// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-20

// Package version holds the build identity, set by the linker:
//
//	-ldflags "-X github.com/OpenXPlay/OpenXPlay-Flasher/internal/version.Version=0.1.0 ..."
package version

var (
	Version = "dev"
	Commit  = ""
)

const (
	Name      = "OpenXPlay Flasher"
	SourceURL = "https://github.com/OpenXPlay/OpenXPlay-Flasher"
	License   = "AGPL-3.0-only"
)
