// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package base

import (
	"context"
	"os"
	"path/filepath"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/catalog"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/profile"
)

// Provider tries the signed catalog first and falls back to imports pinned by the profile.
// Imports live in the cache so a CLI import remains available to later invocations.
type Provider struct {
	Cache *catalog.Cache
	Layer *catalog.Layer
	Dir   string
}

func (p Provider) importPath(pin profile.Archive) string {
	return filepath.Join(p.Dir, "imports", "base", pin.SHA256, pin.File)
}
func (p Provider) Resolve(ctx context.Context, role string, pin profile.Archive, progress catalog.ProgressFunc) (string, string, error) {
	var first error
	if p.Cache != nil && p.Layer != nil {
		for _, a := range p.Layer.Artifacts {
			if a.Role != role {
				continue
			}
			if a.Size != pin.Size || a.SHA256 != pin.SHA256 {
				first = &catalog.IntegrityError{Name: a.Name, Reason: "catalog base differs from profile pins"}
				break
			}
			path, err := p.Cache.Ensure(ctx, a, progress)
			if err == nil {
				return path, "catalog", nil
			}
			first = err
			break
		}
	}
	path := p.importPath(pin)
	if err := verify(ctx, path, pin.Size, pin.SHA256); err == nil {
		return path, "imported", nil
	} else if first == nil {
		first = err
	}
	return "", "", first
}

func (p Provider) Import(ctx context.Context, b *profile.Base, path string) (string, error) {
	if b == nil {
		return "", catalog.ErrNoMatch
	}
	n, sum, err := catalog.Hash(ctx, path)
	if err != nil {
		return "", err
	}
	for _, pin := range b.Archives {
		if pin.Size != n || pin.SHA256 != sum {
			continue
		}
		dest := p.importPath(pin)
		if verify(ctx, dest, n, sum) == nil {
			return dest, nil
		}
		r, err := os.Open(path)
		if err != nil {
			return "", err
		}
		defer r.Close()
		if err = copyPinned(ctx, dest, r, n, sum); err != nil {
			return "", err
		}
		return dest, nil
	}
	return "", catalog.ErrNoMatch
}

func (p Provider) Available(ctx context.Context, role string, pin profile.Archive) bool {
	if verify(ctx, p.importPath(pin), pin.Size, pin.SHA256) == nil {
		return true
	}
	if p.Layer != nil {
		for _, a := range p.Layer.Artifacts {
			if a.Role == role && a.Size == pin.Size && a.SHA256 == pin.SHA256 {
				return true
			}
		}
	}
	return false
}
