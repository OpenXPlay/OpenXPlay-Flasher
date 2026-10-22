// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package privatefile

import (
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Create creates a private file and refuses to overwrite an existing path.
func Create(path string) (*os.File, error) { return open(path, false) }

// Append opens or creates a private file, tightening existing permissions before writing.
func Append(path string) (*os.File, error) { return open(path, true) }

func open(path string, appendOnly bool) (*os.File, error) {
	fail := func(err error) (*os.File, error) {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return fail(err)
	}
	// Protect the DACL from inherited permissions, including in a shared parent directory.
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;" + user.User.Sid.String() + ")")
	if err != nil {
		return fail(err)
	}
	name, err := filepath.Abs(path)
	if err != nil {
		return fail(err)
	}
	if !strings.HasPrefix(name, `\\?\`) {
		if strings.HasPrefix(name, `\\`) {
			name = `\\?\UNC\` + strings.TrimPrefix(name, `\\`)
		} else {
			name = `\\?\` + name
		}
	}
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return fail(err)
	}
	access := uint32(windows.GENERIC_WRITE | windows.READ_CONTROL | windows.WRITE_DAC)
	disposition := uint32(windows.CREATE_NEW)
	attributes := uint32(windows.FILE_ATTRIBUTE_NORMAL | windows.FILE_FLAG_OPEN_REPARSE_POINT)
	if appendOnly {
		access = windows.FILE_APPEND_DATA | windows.FILE_WRITE_ATTRIBUTES | windows.FILE_WRITE_EA | windows.SYNCHRONIZE | windows.READ_CONTROL | windows.WRITE_DAC
		disposition = windows.OPEN_ALWAYS
		attributes = windows.FILE_ATTRIBUTE_NORMAL
	}
	sa := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	h, err := windows.CreateFile(p, access, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, &sa, disposition, attributes, 0)
	if err != nil {
		return fail(err)
	}
	// Creation attributes do not change existing ACLs. Require ACL support before any write.
	dacl, _, err := sd.DACL()
	if err == nil {
		err = windows.SetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
	}
	if err != nil {
		windows.CloseHandle(h)
		if !appendOnly {
			os.Remove(path)
		}
		return fail(err)
	}
	return os.NewFile(uintptr(h), path), nil
}
