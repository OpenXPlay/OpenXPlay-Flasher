// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package privatefile_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/privatefile"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/testfile"
	"golang.org/x/sys/windows"
)

func shareFile(t *testing.T, path, inheritance string) {
	t.Helper()
	sd, err := windows.SecurityDescriptorFromString("D:P(A;" + inheritance + ";FA;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		t.Fatal(err)
	}
}

func TestPrivateFilesInSharedDirectory(t *testing.T) {
	dir := t.TempDir()
	shareFile(t, dir, "OICI")
	path := filepath.Join(dir, "secret")
	f, err := privatefile.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("first\n"); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	testfile.Private(t, path)
	if f, err := privatefile.Create(path); !errors.Is(err, os.ErrExist) {
		if f != nil {
			f.Close()
		}
		t.Fatalf("existing secret overwritten: %v", err)
	}
	shareFile(t, path, "")
	f, err = privatefile.Append(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("second\n"); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	testfile.Private(t, path)
	if data, err := os.ReadFile(path); err != nil || string(data) != "first\nsecond\n" {
		t.Fatalf("append changed existing contents: %q, %v", data, err)
	}
	tmp, err := privatefile.CreateTemp(dir, ".private-")
	if err != nil {
		t.Fatal(err)
	}
	if err := tmp.Close(); err != nil {
		t.Fatal(err)
	}
	testfile.Private(t, tmp.Name())
}
