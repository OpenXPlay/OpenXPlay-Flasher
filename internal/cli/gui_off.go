// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-20

//go:build nogui

package cli

import "errors"

// runGUI: a build with -tags nogui has no Wails and no cgo webview, only the commands.
func runGUI() error {
	return errors.New("this build has no desktop app; run openxplay-flasher help for its commands")
}
