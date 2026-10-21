// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package base

import (
	"errors"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/catalog"
	"os"
	"testing"
)

func TestSelectedBaseFileIsUsedInPlaceAndRehashed(t *testing.T) {
	f := makeFixture(t)
	pin := f.p.Base.Archives["boot"]
	provider := Provider{Paths: map[string]string{pin.SHA256: f.archives["boot"]}}
	path, source, err := provider.Resolve(t.Context(), "boot", pin, nil)
	if err != nil || source != "selected" || path != f.archives["boot"] {
		t.Fatalf("%s %s %v", path, source, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data[0] ^= 1
	put(t, path, data)
	if _, _, err = provider.Resolve(t.Context(), "boot", pin, nil); !errors.Is(err, catalog.ErrIntegrity) {
		t.Fatalf("changed file accepted: %v", err)
	}
	if _, _, err = (Provider{}).Resolve(t.Context(), "boot", pin, nil); err == nil {
		t.Fatal("missing selection accepted")
	}
}
