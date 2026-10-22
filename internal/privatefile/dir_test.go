// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package privatefile_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/privatefile"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/testfile"
)

func TestPrivateStoreDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store")
	if err := privatefile.Dir(path); err != nil {
		t.Fatal(err)
	}
	if err := privatefile.Dir(path); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(path, "secret")
	f, err := privatefile.Create(child)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	testfile.Private(t, child)
	if err := privatefile.Dir(child); err == nil {
		t.Fatal("regular file accepted as private directory")
	}
	link := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(path, link); err != nil {
		t.Skip("symlinks unavailable")
	}
	if err := privatefile.Dir(link); err == nil {
		t.Fatal("symlink accepted as private store")
	}
}
