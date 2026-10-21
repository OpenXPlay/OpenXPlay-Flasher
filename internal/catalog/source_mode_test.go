// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package catalog

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Include timestamps as well as bytes to detect even a rewrite of the verified files.
type cachedFile struct {
	Size     int64
	Mode     fs.FileMode
	Modified time.Time
	Data     string
}

func verifiedFiles(t *testing.T, root string) map[string]cachedFile {
	t.Helper()
	out := map[string]cachedFile{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "unverified" {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		out[path] = cachedFile{info.Size(), info.Mode(), info.ModTime(), string(data)}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestUnverifiedSourceKeepsVerifiedStateAndCache(t *testing.T) {
	for _, tc := range []struct {
		name, reason string
		status       int
		signature    string
		verified     bool
	}{
		{"unsigned", NoSignature, 404, "", false},
		{"bad signature", BadSignature, 200, "bad", false},
		{"empty signature", BadSignature, 200, "", false},
		{"unavailable signature", SignatureOff, 503, "", false},
		{"oversized signature", BadSignature, 200, strings.Repeat("x", MaxSignatureSize+1), false},
		{"valid signature", "", 200, "valid", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			index := testIndex()
			index.Sequence = 8
			body, sig := signedIndex(t, index)
			status := 200
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, ".minisig") {
					w.WriteHeader(status)
					w.Write(sig)
				} else {
					w.Write(body)
				}
			}))
			defer server.Close()
			s := NewSource(t.TempDir(), "test", server.Client())
			s.BaseURLs = []string{server.URL}
			if got, err := s.Fetch(ctx, "beta", Verified); err != nil || !got.Verified {
				t.Fatalf("seed: %+v %v", got, err)
			}
			before := verifiedFiles(t, s.root())
			index.Sequence = 2
			body, sig = signedIndex(t, index)
			status = tc.status
			if tc.signature != "valid" {
				sig = []byte(tc.signature)
			}
			got, err := s.Fetch(ctx, "beta", Unverified)
			if err != nil || got.Sequence != 2 || got.Verified != tc.verified || got.Reason != tc.reason {
				t.Fatalf("lower sequence: %+v %v", got, err)
			}
			// A higher sequence in this mode must not raise the verified high-water mark either.
			index.Sequence = 20
			body, sig = signedIndex(t, index)
			if _, err := s.Fetch(ctx, "beta", Unverified); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, verifiedFiles(t, s.root())) {
				t.Fatal("unverified mode touched verified files")
			}
			index.Sequence = 2
			body, sig = signedIndex(t, index)
			status = tc.status
			if tc.signature != "valid" {
				sig = []byte(tc.signature)
			}
			if _, err := s.Fetch(ctx, "beta", Unverified); err != nil {
				t.Fatal(err)
			}
			if tc.verified {
				if _, err := s.Fetch(ctx, "beta", Verified); !errors.Is(err, ErrRollback) {
					t.Fatalf("switching on did not enforce rollback: %v", err)
				}
			} else if tc.status == 200 {
				if _, err := s.Fetch(ctx, "beta", Verified); err == nil {
					t.Fatal("switching on accepted a bad signature")
				}
			}
			server.Close()
			// New instances must pick the cache for the explicitly requested mode.
			s = NewSource(s.DataDir, "test", server.Client())
			s.BaseURLs = []string{server.URL}
			got, err = s.Fetch(ctx, "beta", Unverified)
			if err != nil || !s.Offline() || got.Sequence != 2 || got.Verified != tc.verified || got.Reason != tc.reason {
				t.Fatalf("offline unverified: %+v %v", got, err)
			}
			got, err = s.LoadCached(ctx, "beta", Verified)
			if err != nil || !got.Verified || got.Sequence != 8 {
				t.Fatalf("restored verified cache: %+v %v", got, err)
			}
			if !reflect.DeepEqual(before, verifiedFiles(t, s.root())) {
				t.Fatal("offline mode touched verified files")
			}
		})
	}
}

func TestUnverifiedSourceStillStrictlyValidatesIndex(t *testing.T) {
	body := indexBytes(t, testIndex())
	for _, bad := range [][]byte{
		append(bytes.Clone(body), []byte(` {}`)...),
		bytes.Replace(body, []byte(`"schema":`), []byte(`"unknown":0,"schema":`), 1),
		bytes.Replace(body, []byte(`"channel":"beta"`), []byte(`"channel":"stable"`), 1),
		bytes.Replace(body, []byte(`"sequence":1`), []byte(`"sequence":0`), 1),
		bytes.Replace(body, []byte(`"sequence":1`), []byte(`"sequence":1,"sequence":1`), 1),
		bytes.Repeat([]byte("x"), MaxIndexSize+1),
	} {
		t.Run(string(bad[:min(30, len(bad))]), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(bad) }))
			defer server.Close()
			s := NewSource(t.TempDir(), "test", server.Client())
			s.BaseURLs = []string{server.URL}
			if _, err := s.Fetch(t.Context(), "beta", Unverified); err == nil {
				t.Fatal("invalid index accepted")
			}
		})
	}
}

func TestUnverifiedSourceDoesNotReadVerifiedState(t *testing.T) {
	body := indexBytes(t, testIndex())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".minisig") {
			http.NotFound(w, r)
			return
		}
		w.Write(body)
	}))
	defer server.Close()
	s := NewSource(t.TempDir(), "test", server.Client())
	s.BaseURLs = []string{server.URL}
	path := filepath.Join(s.root(), "state.json")
	putFile(t, path, []byte("invalid verified state"))
	if _, err := s.Fetch(context.Background(), "beta", Unverified); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Fetch(context.Background(), "beta", Verified); err == nil {
		t.Fatal("verified mode ignored corrupt state")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "invalid verified state" {
		t.Fatal("verified state overwritten", err)
	}
}
