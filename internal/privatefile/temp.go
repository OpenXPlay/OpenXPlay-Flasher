// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

// Package privatefile creates user-only files using POSIX permissions or Windows ACLs.
package privatefile

import (
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
)

// CreateTemp creates an exclusive private file with a random name beginning with prefix.
func CreateTemp(dir, prefix string) (*os.File, error) {
	if dir == "" {
		dir = os.TempDir()
	}
	for range 10 {
		f, err := Create(filepath.Join(dir, prefix+rand.Text()))
		if !errors.Is(err, os.ErrExist) {
			return f, err
		}
	}
	return nil, &os.PathError{Op: "createtemp", Path: dir, Err: os.ErrExist}
}
