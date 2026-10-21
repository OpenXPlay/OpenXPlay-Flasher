// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package catalog

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestSourceStateOfflineAndRollback(t *testing.T) {
	ctx := context.Background()
	i := testIndex()
	i.Sequence = 4
	body, sig := signedIndex(t, i)
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Header.Get("User-Agent") != "OpenXPlay-Flasher/1.2.3" {
			t.Error("missing versioned User-Agent")
		}
		if strings.HasSuffix(r.URL.Path, ".minisig") {
			w.Write(sig)
		} else {
			w.Write(body)
		}
	}))
	t.Cleanup(server.Close)
	t.Setenv("OXP_FLASHER_CATALOG_URL", server.URL+"/{channel}/")
	dir := t.TempDir()
	s := NewSource(dir, "1.2.3", server.Client())
	if got, err := s.Fetch(ctx, "beta"); err != nil || got.Sequence != 4 {
		t.Fatalf("%+v, %v", got, err)
	}
	// A new Source must observe the high-water mark, even with no in-memory state.
	i.Sequence = 3
	newBody, newSig := signedIndex(t, i)
	mu.Lock()
	body, sig = newBody, newSig
	mu.Unlock()
	s = NewSource(dir, "1.2.3", server.Client())
	_, err := s.Fetch(ctx, "beta")
	var rollback *RollbackError
	if !errors.Is(err, ErrRollback) || !errors.As(err, &rollback) || rollback.Highest != 4 {
		t.Fatal(err)
	}
	server.Close()
	if got, err := NewSource(dir, "1.2.3", server.Client()).Fetch(ctx, "beta"); err != nil || got.Sequence != 4 {
		t.Fatalf("offline: %+v, %v", got, err)
	}
	state, err := s.readState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(s.root(), "beta", state.Channels["beta"].Digest, "index.json")
	data, err := os.ReadFile(cache)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cache, bytes.Replace(data, []byte("openstick-v1"), []byte("openstick-v9"), 1), 0o600); err != nil {
		t.Fatal(err)
	}
	var signature *SignatureError
	if _, err := s.LoadCached(ctx, "beta"); !errors.As(err, &signature) {
		t.Fatalf("tampered offline index: %v", err)
	}
}

func TestSourceFailures(t *testing.T) {
	body, sig := signedIndex(t, testIndex())
	for _, tc := range []struct {
		name      string
		body, sig []byte
		tooLarge  bool
	}{
		{"tampered", bytes.Replace(body, []byte("beta"), []byte("evil"), 1), sig, false},
		{"signature", body, []byte("garbage"), false},
		{"oversized index", bytes.Repeat([]byte("x"), MaxIndexSize+1), sig, true},
		{"oversized signature", body, bytes.Repeat([]byte("x"), MaxSignatureSize+1), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Flush before writing to exercise the streaming limit without Content-Length.
				w.(http.Flusher).Flush()
				if strings.HasSuffix(r.URL.Path, ".minisig") {
					w.Write(tc.sig)
				} else {
					w.Write(tc.body)
				}
			}))
			defer server.Close()
			s := NewSource(t.TempDir(), "test", server.Client())
			s.BaseURLs = []string{server.URL}
			// Seed an offline copy: verification errors must remain visible despite its presence.
			i := testIndex()
			state, err := s.readState(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if err := s.accept(context.Background(), i, body, sig, state); err != nil {
				t.Fatal(err)
			}
			_, err = s.Fetch(context.Background(), "beta")
			if tc.tooLarge {
				if !errors.Is(err, ErrTooLarge) {
					t.Fatal(err)
				}
			} else {
				var invalid *SignatureError
				if !errors.As(err, &invalid) {
					t.Fatal(err)
				}
			}
		})
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer server.Close()
	s := NewSource(t.TempDir(), "test", server.Client())
	s.BaseURLs = []string{server.URL}
	if _, err := s.Fetch(context.Background(), "beta"); err == nil {
		t.Fatal("unavailable index accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Fetch(ctx, "beta"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := s.Fetch(context.Background(), "../beta"); err == nil {
		t.Fatal("invalid channel accepted")
	}
}

func TestSourceMirrorsChannelsAndCorruptState(t *testing.T) {
	body, sig := signedIndex(t, testIndex())
	bad := httptest.NewServer(http.NotFoundHandler())
	defer bad.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".minisig") {
			w.Write(sig)
		} else {
			w.Write(body)
		}
	}))
	defer good.Close()
	s := NewSource(t.TempDir(), "test", good.Client())
	s.BaseURLs = []string{bad.URL, good.URL}
	if _, err := s.Fetch(context.Background(), "beta"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Fetch(context.Background(), "stable"); err == nil {
		t.Fatal("wrong channel accepted")
	}
	for _, content := range []string{`{"channels":null}`, `{}`, `null`} {
		if err := os.WriteFile(filepath.Join(s.root(), "state.json"), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Fetch(context.Background(), "beta"); err == nil {
			t.Fatal("corrupt state silently reset")
		}
	}
}

func TestSourceRepairsCachedPair(t *testing.T) {
	body, sig := signedIndex(t, testIndex())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".minisig") {
			w.Write(sig)
		} else {
			w.Write(body)
		}
	}))
	defer server.Close()
	s := NewSource(t.TempDir(), "test", server.Client())
	s.BaseURLs = []string{server.URL}
	ctx := context.Background()
	if _, err := s.Fetch(ctx, "beta"); err != nil {
		t.Fatal(err)
	}
	state, err := s.readState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(s.root(), "beta", state.Channels["beta"].Digest)
	putFile(t, filepath.Join(dir, "index.json"), []byte("damaged"))
	putFile(t, filepath.Join(dir, "index.json.minisig"), []byte("damaged"))
	if _, err := s.Fetch(ctx, "beta"); err != nil {
		t.Fatal(err)
	}
	server.Close()
	if _, err := s.LoadCached(ctx, "beta"); err != nil {
		t.Fatal(err)
	}
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
