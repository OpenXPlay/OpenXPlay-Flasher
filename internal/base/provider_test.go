// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package base

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/catalog"
)

func TestCatalogFirstAndPinnedImportFallback(t *testing.T) {
	f := makeFixture(t)
	data, err := os.ReadFile(f.archives["boot"])
	if err != nil {
		t.Fatal(err)
	}
	requests := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { requests++; w.Write(data) }))
	defer server.Close()
	pin := f.p.Base.Archives["boot"]
	artifact := catalog.Artifact{Role: "boot", Name: pin.File, Size: pin.Size, SHA256: pin.SHA256, URLs: []string{server.URL + "/boot"}}
	cache := catalog.NewCache(t.TempDir(), nil, server.Client())
	provider := Provider{Dir: t.TempDir(), Cache: cache, Layer: &catalog.Layer{Artifacts: []catalog.Artifact{artifact}}}
	imported, err := provider.Import(t.Context(), f.p.Base, f.archives["boot"])
	if err != nil {
		t.Fatal(err)
	}
	var total int64
	path, source, err := provider.Resolve(t.Context(), "boot", pin, func(p catalog.Progress) { total = p.Done })
	if err != nil || source != "catalog" || path == imported || requests != 1 || total != pin.Size {
		t.Fatalf("catalog was not first: %s %s %d %d %v", path, source, requests, total, err)
	}
	// When the cache and mirror are unavailable, the profile-pinned import is independent.
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	server.Close()
	path, source, err = provider.Resolve(t.Context(), "boot", pin, nil)
	if err != nil || source != "imported" || path != imported {
		t.Fatalf("import fallback: %s %s %v", path, source, err)
	}
	provider.Layer = nil
	provider.Cache = nil
	if _, source, err = provider.Resolve(t.Context(), "boot", pin, nil); err != nil || source != "imported" {
		t.Fatal("offline without catalog", err)
	}
	renamed := filepath.Join(t.TempDir(), "any-name.bin")
	put(t, renamed, data)
	if _, err = provider.Import(context.Background(), f.p.Base, renamed); err != nil {
		t.Fatal(err)
	}
	put(t, renamed, []byte("unmatched"))
	if _, err = provider.Import(t.Context(), f.p.Base, renamed); !errors.Is(err, catalog.ErrNoMatch) {
		t.Fatal("unmatched archive accepted", err)
	}
}

func TestCatalogCannotOverrideProfilePins(t *testing.T) {
	f := makeFixture(t)
	pin := f.p.Base.Archives["boot"]
	provider := Provider{Dir: t.TempDir(), Cache: catalog.NewCache(t.TempDir(), nil, nil), Layer: &catalog.Layer{Artifacts: []catalog.Artifact{{Role: "boot", Size: pin.Size, SHA256: hash([]byte("different"))}}}}
	if _, _, err := provider.Resolve(t.Context(), "boot", pin, nil); !errors.Is(err, catalog.ErrIntegrity) {
		t.Fatalf("catalog overrode profile: %v", err)
	}
}
