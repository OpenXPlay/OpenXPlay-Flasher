// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package base

import (
	"context"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/catalog"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/profile"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/stage"
)

// Provider resolves user-selected files in place. It has no network or copying capability.
// Files checks every archive against the profile pins again before extraction.
type Provider struct{ Paths map[string]string }

func (p Provider) Resolve(ctx context.Context, role string, pin profile.Archive, progress catalog.ProgressFunc) (string, string, error) {
	filename := p.Paths[pin.SHA256]
	if filename == "" {
		return "", "", &stage.Problem{Code: "base-files", Detail: pin.File}
	}
	if err := verify(ctx, filename, pin.Size, pin.SHA256); err != nil {
		return "", "", err
	}
	if progress != nil {
		progress(catalog.Progress{Done: pin.Size, Total: pin.Size})
	}
	return filename, "selected", nil
}
