// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-20

//go:build !dev

package gui

// readAsset reads a page file from the copy embedded in the binary.
func readAsset(name string) ([]byte, error) {
	return assets.ReadFile("assets/" + name)
}
