// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-20

//go:build !windows && !nogui

package gui

import "github.com/wailsapp/wails/v3/pkg/application"

// webView2Check: only Windows needs a separately installed webview runtime.
func webView2Check(*application.App, string) bool { return true }
