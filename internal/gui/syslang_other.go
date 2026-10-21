// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-20

//go:build !darwin && !windows

package gui

import "os"

// systemLang reads the POSIX locale variables in their order of precedence.
func systemLang() string {
	for _, key := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if v := os.Getenv(key); v != "" && v != "C" && v != "POSIX" {
			return v
		}
	}
	return ""
}
