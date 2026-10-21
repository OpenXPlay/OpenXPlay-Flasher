// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/catalog"
)

func sourceFixture(t *testing.T, dir string) string {
	t.Helper()
	file := filepath.Join(dir, "local bytes")
	if err := os.WriteFile(file, []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	source := sourceIndex{Schema: catalog.Schema, Channel: "beta", Sequence: 1, Devices: map[string]sourceDevice{
		"ufi003": {Recommended: map[string]string{"base": "opaque-v1"}, Layers: []sourceLayer{{Layer: "base", Version: "opaque-v1", Source: "https://example.org/v1", Artifacts: []sourceArtifact{{Role: "boot", Name: "boot.img", File: file, URLs: []string{"https://example.org/boot.img"}}}}}},
	}}
	path := filepath.Join(dir, "source.json")
	writeSource(t, path, source)
	return path
}

func writeSource(t *testing.T, path string, source sourceIndex) {
	t.Helper()
	b, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestToolEndToEnd(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	pub, secret := filepath.Join(dir, "key.pub"), filepath.Join(dir, "key.secret")
	invoke := func(in io.Reader, args ...string) error {
		return run(ctx, args, in, io.Discard, catalog.NewHTTPClient())
	}
	if err := invoke(nil, "keygen", "--public", pub, "--secret", secret); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(secret)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("secret mode: %v %v", st, err)
	}
	key, err := os.ReadFile(secret)
	if err != nil {
		t.Fatal(err)
	}
	pubBytes, err := os.ReadFile(pub)
	if err != nil {
		t.Fatal(err)
	}
	if err := invoke(nil, "keygen", "--public", pub, "--secret", secret); err == nil {
		t.Fatal("overwrote keys")
	}
	if b, _ := os.ReadFile(secret); !bytes.Equal(b, key) {
		t.Fatal("existing secret changed")
	}
	if b, _ := os.ReadFile(pub); !bytes.Equal(b, pubBytes) {
		t.Fatal("existing public changed")
	}
	// A collision with only the secret also leaves the old secret intact and no new public file.
	otherPub := filepath.Join(dir, "other.pub")
	if err := invoke(nil, "keygen", "--public", otherPub, "--secret", secret); err == nil {
		t.Fatal("overwrote secret")
	}
	if _, err := os.Stat(otherPub); !os.IsNotExist(err) {
		t.Fatal("orphan public key")
	}
	source := sourceFixture(t, dir)
	out := filepath.Join(dir, "out")
	index := filepath.Join(out, "index.json")
	if err := invoke(nil, "build", "--source", source, "--out", out); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	if data[len(data)-1] != '\n' || !bytes.Contains(data, []byte("\n  \"schema\"")) || bytes.Contains(data, []byte(`"file":`)) {
		t.Fatalf("unexpected encoding: %s", data)
	}
	i, err := catalog.Decode(data)
	if err != nil || i.Artifacts()[0].Size != 3 || i.Artifacts()[0].SHA256 != fmt.Sprintf("%x", sha256.Sum256([]byte("abc"))) {
		t.Fatalf("%+v, %v", i, err)
	}
	if err := invoke(nil, "sign", "--secret", secret, "--index", index); err != nil {
		t.Fatal(err)
	}
	if err := invoke(nil, "verify", "--index", index, "--public", pub); err != nil {
		t.Fatal(err)
	}
	if err := invoke(nil, "verify", "--index", index); err == nil {
		t.Fatal("new key trusted by client")
	}
	if err := invoke(bytes.NewReader(key), "sign", "--secret", "-", "--index", index); err != nil {
		t.Fatal(err)
	}
	if err := invoke(nil, "verify", "--index", index, "--public", pub); err != nil {
		t.Fatal(err)
	}
	if err := invoke(nil, "build", "--source", source, "--out", out, "--previous", index); !errors.Is(err, catalog.ErrRollback) {
		t.Fatal(err)
	}
	var s sourceIndex
	b, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	for _, seq := range []uint64{0, 2} {
		s.Sequence = seq
		writeSource(t, source, s)
		err := invoke(nil, "build", "--source", source, "--out", filepath.Join(dir, "next"), "--previous", index)
		if seq == 0 && !errors.Is(err, catalog.ErrRollback) {
			t.Fatal(err)
		}
		if seq == 2 && err != nil {
			t.Fatal(err)
		}
	}
}

func TestBuildFetchAndSourceErrors(t *testing.T) {
	dir := t.TempDir()
	path := sourceFixture(t, dir)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var source sourceIndex
	if err := json.Unmarshal(b, &source); err != nil {
		t.Fatal(err)
	}
	var calls int
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; io.WriteString(w, "abc") }))
	defer server.Close()
	a := &source.Devices["ufi003"].Layers[0].Artifacts[0]
	a.File = ""
	a.URLs = []string{server.URL + "/0.1~beta.deb"}
	writeSource(t, path, source)
	if _, err := build(context.Background(), path, "", false, server.Client(), time.Now()); err == nil {
		t.Fatal("fetch was not required")
	}
	if calls != 0 {
		t.Fatal("downloaded without --fetch")
	}
	i, err := build(context.Background(), path, "", true, server.Client(), time.Unix(0, 0))
	if err != nil || i.Generated != "1970-01-01T00:00:00Z" || i.Artifacts()[0].Size != 3 {
		t.Fatalf("%+v %v", i, err)
	}
	if calls != 1 {
		t.Fatal(calls)
	}
	for _, mode := range []string{"only size", "only hash", "mixed file", "unknown field"} {
		t.Run(mode, func(t *testing.T) {
			var copySource sourceIndex
			// Round-trip to detach nested slices before each mutation.
			b, _ := json.Marshal(source)
			if err := json.Unmarshal(b, &copySource); err != nil {
				t.Fatal(err)
			}
			a := &copySource.Devices["ufi003"].Layers[0].Artifacts[0]
			size, hash := int64(3), strings.Repeat("0", 64)
			switch mode {
			case "only size":
				a.Size = &size
			case "only hash":
				a.SHA256 = &hash
			case "mixed file":
				a.File = "local"
				a.Size = &size
			}
			writeSource(t, path, copySource)
			if mode == "unknown field" {
				b, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, bytes.Replace(b, []byte(`"role":`), []byte(`"unknown":1,"role":`), 1), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := build(context.Background(), path, "", true, server.Client(), time.Now()); err == nil {
				t.Fatal("invalid source accepted")
			}
		})
	}
}

func TestCommandErrors(t *testing.T) {
	for _, args := range [][]string{nil, {"unknown"}, {"keygen"}, {"build"}, {"sign"}, {"verify"}, {"verify", "--unknown"}, {"build", "extra"}} {
		if err := run(context.Background(), args, bytes.NewReader(nil), io.Discard, nil); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}
