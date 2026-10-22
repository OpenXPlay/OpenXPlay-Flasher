// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

//go:build !windows

// Package testfile checks private file permissions in integration tests.
package testfile

import (
	"os"
	"testing"
)

func Private(t *testing.T, path string) {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Mode().IsRegular() || st.Mode().Perm() != 0o600 {
		t.Fatalf("%s: expected a private regular file, got %v", path, st.Mode())
	}
}
