// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package app

import (
	"runtime"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/adb"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/version"
)

// Info identifies the build and host, for the about view and diagnostics.
type Info struct {
	Name         string             `json:"name"`
	Version      string             `json:"version"`
	Commit       string             `json:"commit"`
	BuildDate    string             `json:"buildDate,omitempty"`
	License      string             `json:"license"`
	SourceURL    string             `json:"sourceUrl"`
	Platform     string             `json:"platform"`
	Architecture string             `json:"architecture"`
	OSVersion    string             `json:"osVersion,omitempty"`
	ADB          adb.ConnectionInfo `json:"adb"`
	// SettingsFile is supplied by the GUI shell, which owns the settings store.
	SettingsFile string `json:"settingsFile,omitempty"`
}

// Info reads local metadata only; it never connects to a device or an adb server.
func (s *Service) Info() Info {
	s.mu.Lock()
	connector := s.adb
	s.mu.Unlock()
	info := Info{
		Name: version.Name, Version: version.Version, Commit: version.Commit,
		BuildDate: version.BuildDate, License: version.License, SourceURL: version.SourceURL,
		Platform: runtime.GOOS, Architecture: runtime.GOARCH, OSVersion: osVersion(),
	}
	if c, ok := connector.(interface{ Info() adb.ConnectionInfo }); ok {
		info.ADB = c.Info()
	}
	return info
}
