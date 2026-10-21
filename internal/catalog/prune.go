// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package catalog

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// history retains two versions per profile/layer across index replacements. Since versions are
// opaque, "newest" uses the publisher's newest-first convention, with previously seen versions
// following versions in the current index. No lexical or Debian version comparison is attempted.
type history map[string][]Layer

func (c *Cache) history(ctx context.Context) (history, error) {
	h := make(history)
	b, err := ReadFile(ctx, filepath.Join(c.Dir, "catalog-history.json"), 8*MaxIndexSize)
	if os.IsNotExist(err) {
		return h, nil
	}
	if err != nil {
		return nil, err
	}
	if err := DecodeStrict(b, &h); err != nil {
		return nil, err
	}
	if h == nil {
		return nil, &InvalidError{"cache history", "missing object"}
	}
	for _, layers := range h {
		for _, l := range layers {
			for _, a := range l.Artifacts {
				if err := a.Validate(); err != nil {
					return nil, err
				}
			}
		}
	}
	return h, nil
}

func (c *Cache) remember(ctx context.Context) error {
	if c.Index == nil {
		return nil
	}
	if err := c.Index.Validate(); err != nil {
		return err
	}
	h, err := c.history(ctx)
	if err != nil {
		return err
	}
	for id, d := range c.Index.Devices {
		all := append(append([]Layer(nil), d.Layers...), h[id]...)
		seen := make(map[string]map[string]bool)
		var keep []Layer
		for _, l := range all {
			if seen[l.Layer] == nil {
				seen[l.Layer] = make(map[string]bool)
			}
			if seen[l.Layer][l.Version] || len(seen[l.Layer]) >= 2 {
				continue
			}
			seen[l.Layer][l.Version] = true
			keep = append(keep, l)
		}
		h[id] = keep
	}
	b, err := json.MarshalIndent(h, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(c.Dir, 0o700); err != nil {
		return err
	}
	return WriteFile(ctx, filepath.Join(c.Dir, "catalog-history.json"), append(b, '\n'), 0o600)
}

// Prune keeps everything in the current index plus the two newest observed versions of each
// layer. Unfinished transfers have a one-week grace period even when no longer referenced.
// Call it after changing Index as well as during routine cache maintenance.
func (c *Cache) Prune(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.Index == nil {
		return &InvalidError{"index", "no current index"}
	}
	if err := c.remember(ctx); err != nil {
		return err
	}
	h, err := c.history(ctx)
	if err != nil {
		return err
	}
	root := filepath.Join(c.Dir, "artifacts")
	keep := make(map[string]bool)
	add := func(a Artifact) { keep[filepath.Join(a.SHA256, a.Name)] = true }
	for _, a := range c.Index.Artifacts() {
		add(a)
	}
	for _, layers := range h {
		for _, l := range layers {
			for _, a := range l.Artifacts {
				add(a)
			}
		}
	}
	cutoff := time.Now().Add(-7 * 24 * time.Hour)
	var dirs []string
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if os.IsNotExist(err) && path == root {
			return nil
		}
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.IsDir() {
			if path != root {
				dirs = append(dirs, path)
			}
			return nil
		}
		rel, ok := parseCachePath(root, path)
		if !ok || keep[rel] {
			return nil
		}
		if strings.HasSuffix(d.Name(), ".part") {
			st, err := d.Info()
			if err != nil {
				return err
			}
			if !st.ModTime().Before(cutoff) {
				return nil
			}
		}
		return os.Remove(path)
	})
	if err != nil {
		return err
	}
	for j := len(dirs) - 1; j >= 0; j-- {
		entries, err := os.ReadDir(dirs[j])
		if err != nil {
			return err
		}
		if len(entries) == 0 {
			if err := os.Remove(dirs[j]); err != nil {
				return err
			}
		}
	}
	return nil
}
