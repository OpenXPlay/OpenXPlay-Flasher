// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-20

//go:build !nogui

package cli

import "github.com/OpenXPlay/OpenXPlay-Flasher/internal/gui"

func runGUI() error { return gui.Run() }
