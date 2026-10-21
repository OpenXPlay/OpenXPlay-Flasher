// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-20

//go:build dev

package gui

import (
	"os"
	"path/filepath"
	"runtime"
)

// assetDir is internal/gui/assets in the source tree, so a dev build serves edits on reload
// without rebuilding.
var assetDir = func() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "assets")
}()

func readAsset(name string) ([]byte, error) {
	return os.ReadFile(filepath.Join(assetDir, filepath.FromSlash(name)))
}
