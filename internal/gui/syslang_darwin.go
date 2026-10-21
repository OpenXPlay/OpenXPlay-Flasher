// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-20

package gui

import (
	"os/exec"
	"strings"
)

// systemLang returns the first of the user's preferred languages. WKWebView's navigator.language
// follows the app bundle's localizations rather than the system, so the page cannot ask itself.
func systemLang() string {
	out, err := exec.Command("defaults", "read", "-g", "AppleLanguages").Output()
	if err != nil {
		return ""
	}
	// The output is a plist array: ( "zh-Hans-CN", "en-US" ).
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.Trim(strings.TrimSpace(line), `",()`)
		if line != "" {
			return line
		}
	}
	return ""
}
