// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-20

package cli

import (
	"os"
	"syscall"
)

// The Windows app is linked as a GUI program (-H windowsgui), so a double-click opens no console
// and the process starts without standard streams. When a command is run from a terminal, attach
// to that terminal's console so it prints; redirected output already has a target.
func init() {
	if len(os.Args) < 2 {
		return
	}
	if t, err := syscall.GetFileType(syscall.Stdout); err == nil && t != 0 {
		return // 0 is FILE_TYPE_UNKNOWN: no stream attached
	}
	const attachParentProcess = ^uintptr(0)
	if r, _, _ := syscall.NewLazyDLL("kernel32.dll").NewProc("AttachConsole").Call(attachParentProcess); r == 0 {
		return
	}
	if out, err := os.OpenFile("CONOUT$", os.O_RDWR, 0); err == nil {
		os.Stdout, os.Stderr = out, out
	}
	if in, err := os.OpenFile("CONIN$", os.O_RDWR, 0); err == nil {
		os.Stdin = in
	}
}
