// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package testfile

import (
	"os"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func Private(t *testing.T, path string) {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil || !st.Mode().IsRegular() {
		t.Fatalf("%s: expected a regular file: %v", path, err)
	}
	// Windows FileMode reports the read-only attribute, not who can access the file.
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	control, _, err := sd.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatalf("%s: ACL permits inheritance: %v", path, err)
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil || dacl.AceCount != 1 {
		t.Fatalf("%s: expected exactly one user access entry: %v", path, err)
	}
	var ace *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(dacl, 0, &ace); err != nil {
		t.Fatal(err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
	want := windows.ACCESS_MASK(windows.FILE_GENERIC_READ | windows.FILE_GENERIC_WRITE)
	if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || !sid.Equals(user.User.Sid) || ace.Mask&want != want {
		t.Fatalf("%s: ACL does not restrict access to the current user", path)
	}
}
