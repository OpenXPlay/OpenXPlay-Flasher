// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package catalog

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func artifactFor(data []byte, urls ...string) Artifact {
	return Artifact{Role: "boot", Name: "boot.img", Size: int64(len(data)), SHA256: fmt.Sprintf("%x", sha256.Sum256(data)), URLs: urls}
}

func putFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDownloads(t *testing.T) {
	data := []byte("verified artifact bytes")
	for _, mode := range []string{"full", "resume", "ignores range", "bad prefix", "bad range", "404 mirror", "hash mirror", "corrupt cache", "complete part", "oversize part"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			var sawRange atomic.Bool
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Header.Get("User-Agent") != "OpenXPlay-Flasher/test" {
					t.Error("missing User-Agent")
				}
				if r.URL.Path == "/404" {
					w.WriteHeader(404)
					return
				}
				if r.URL.Path == "/hash" {
					w.Write(bytes.Repeat([]byte("x"), len(data)))
					return
				}
				if r.Header.Get("Range") != "" {
					sawRange.Store(true)
					if r.Header.Get("Range") != "bytes=5-" {
						t.Error(r.Header.Get("Range"))
					}
					if mode != "ignores range" {
						rangeHeader := fmt.Sprintf("bytes 5-%d/%d", len(data)-1, len(data))
						if mode == "bad range" {
							rangeHeader = "bytes 6-8/9"
						}
						w.Header().Set("Content-Range", rangeHeader)
						w.WriteHeader(206)
						w.Write(data[5:])
						return
					}
				}
				w.Write(data)
			}))
			defer server.Close()
			urls := []string{server.URL + "/good"}
			if mode == "404 mirror" {
				urls = append([]string{server.URL + "/404"}, urls...)
			}
			if mode == "hash mirror" {
				urls = append([]string{server.URL + "/hash"}, urls...)
			}
			a := artifactFor(data, urls...)
			c := NewCache(t.TempDir(), nil, server.Client())
			c.Version = "test"
			switch mode {
			case "resume", "ignores range", "bad range":
				putFile(t, c.path(a)+".part", data[:5])
			case "bad prefix":
				putFile(t, c.path(a)+".part", []byte("wrong"))
			case "corrupt cache":
				putFile(t, c.path(a), bytes.Repeat([]byte("x"), len(data)))
			case "complete part":
				putFile(t, c.path(a)+".part", data)
			case "oversize part":
				putFile(t, c.path(a)+".part", append(bytes.Clone(data), 'x'))
			}
			var last Progress
			p, err := c.Ensure(context.Background(), a, func(p Progress) {
				last = p
				if p.Done > p.Total {
					t.Error(p)
				}
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := Verify(p, a); err != nil {
				t.Fatal(err)
			}
			if mode != "complete part" && (last.Done != a.Size || last.URL != server.URL+"/good") {
				t.Fatal(last)
			}
			if _, err := os.Stat(p + ".part"); !os.IsNotExist(err) {
				t.Fatal("part left after success", err)
			}
			if mode == "resume" && !sawRange.Load() {
				t.Fatal("did not request resume")
			}
			before := calls.Load()
			if _, err := c.Ensure(context.Background(), a, nil); err != nil {
				t.Fatal(err)
			}
			if calls.Load() != before {
				t.Fatal("cached hit made a request")
			}
			want := int32(1)
			if mode == "bad prefix" || mode == "bad range" || mode == "404 mirror" || mode == "hash mirror" {
				want = 2
			}
			if mode == "complete part" {
				want = 0
			}
			if before != want {
				t.Fatalf("requests = %d, want %d", before, want)
			}
		})
	}
}

func TestDownloadOversizeAndRetries(t *testing.T) {
	for _, mode := range []string{"length oversize", "stream oversize", "503", "429", "truncated", "404", "bad hash"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				switch mode {
				case "503", "429", "404":
					code, _ := strconv.Atoi(mode)
					w.WriteHeader(code)
				case "length oversize":
					w.Header().Set("Content-Length", "100000")
					io.WriteString(w, "abcd")
				case "stream oversize":
					w.(http.Flusher).Flush()
					io.WriteString(w, strings.Repeat("x", 100000))
				case "truncated":
					w.Header().Set("Content-Length", "3")
					io.WriteString(w, "a")
				case "bad hash":
					io.WriteString(w, "xyz")
				}
			}))
			defer server.Close()
			c := NewCache(t.TempDir(), nil, server.Client())
			a := artifactFor([]byte("abc"), server.URL)
			_, err := c.Ensure(context.Background(), a, nil)
			if err == nil {
				t.Fatal("accepted invalid body")
			}
			if strings.Contains(mode, "oversize") || mode == "bad hash" {
				if !errors.Is(err, ErrIntegrity) {
					t.Fatal(err)
				}
				if _, err := os.Stat(c.path(a) + ".part"); !os.IsNotExist(err) {
					t.Fatal("bad part retained")
				}
			}
			want := int32(1)
			if mode == "503" || mode == "429" || mode == "truncated" {
				want = 3
			}
			if calls.Load() != want {
				t.Fatalf("%d calls, want %d: %v", calls.Load(), want, err)
			}
		})
	}
}

func TestCancellationKeepsPartAndResumes(t *testing.T) {
	data := bytes.Repeat([]byte("a"), 1<<20)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "" {
			var start int
			fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-", &start)
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, len(data)-1, len(data)))
			w.WriteHeader(206)
			w.Write(data[start:])
			return
		}
		w.Write(data[:4096])
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	a := artifactFor(data, server.URL)
	c := NewCache(t.TempDir(), nil, server.Client())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err := c.Ensure(ctx, a, func(p Progress) {
		if p.Done > 0 {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	st, err := os.Stat(c.path(a) + ".part")
	if err != nil || st.Size() == 0 || st.Size() >= a.Size {
		t.Fatalf("part: %v, %v", st, err)
	}
	if _, err := c.Ensure(context.Background(), a, nil); err != nil {
		t.Fatal(err)
	}
}

func TestImportAndVerify(t *testing.T) {
	i := testIndex()
	c := NewCache(t.TempDir(), i, nil)
	path := filepath.Join(t.TempDir(), "any name at all")
	putFile(t, path, []byte("abc"))
	dest, err := c.Import(context.Background(), path)
	if err != nil || dest != c.path(i.Artifacts()[0]) {
		t.Fatalf("%s, %v", dest, err)
	}
	if err := Verify(dest, i.Artifacts()[0]); err != nil {
		t.Fatal(err)
	}
	putFile(t, path, []byte("xyz"))
	if _, err := c.Import(context.Background(), path); !errors.Is(err, ErrNoMatch) {
		t.Fatal(err)
	}
	if err := Verify(path, i.Artifacts()[0]); !errors.Is(err, ErrIntegrity) {
		t.Fatal(err)
	}
	putFile(t, path, []byte("abcd"))
	if err := Verify(path, i.Artifacts()[0]); !errors.Is(err, ErrIntegrity) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Import(ctx, path); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestPruneRetainsVersionsAndRecentPartials(t *testing.T) {
	c := NewCache(t.TempDir(), nil, nil)
	var artifacts []Artifact
	for _, version := range []string{"one", "two", "three"} {
		i := testIndex()
		d := i.Devices["ufi003"]
		d.Layers[0].Version = version
		d.Recommended["base"] = version
		a := artifactFor([]byte(version), "https://example.org/file")
		d.Layers[0].Artifacts = []Artifact{a}
		i.Devices["ufi003"] = d
		c.Index = i
		artifacts = append(artifacts, a)
		putFile(t, c.path(a), []byte(version))
		if err := c.Prune(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(c.path(artifacts[0])); !os.IsNotExist(err) {
		t.Fatal("old version kept")
	}
	for _, a := range artifacts[1:] {
		if _, err := os.Stat(c.path(a)); err != nil {
			t.Fatal(err)
		}
	}
	oldPart := c.path(artifacts[2]) + ".part"
	recentPart := c.path(artifacts[0]) + ".part"
	putFile(t, oldPart, []byte("x"))
	putFile(t, recentPart, []byte("x"))
	old := time.Now().Add(-8 * 24 * time.Hour)
	if err := os.Chtimes(oldPart, old, old); err != nil {
		t.Fatal(err)
	}
	// Restart the cache; retention must survive as well.
	c = NewCache(c.Dir, c.Index, nil)
	if err := c.Prune(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(oldPart); !os.IsNotExist(err) {
		t.Fatal("stale part kept")
	}
	if _, err := os.Stat(recentPart); err != nil {
		t.Fatal("recent part removed", err)
	}
	// Every version referenced by the current index survives, even beyond the history limit.
	i := testIndex()
	d := i.Devices["ufi003"]
	for j, a := range artifacts {
		d.Layers = append(d.Layers, Layer{Layer: "base", Version: fmt.Sprint(j), Artifacts: []Artifact{a}})
		putFile(t, c.path(a), []byte("bytes"))
	}
	i.Devices["ufi003"] = d
	c.Index = i
	if err := c.Prune(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, a := range artifacts {
		if _, err := os.Stat(c.path(a)); err != nil {
			t.Fatal(err)
		}
	}
}
