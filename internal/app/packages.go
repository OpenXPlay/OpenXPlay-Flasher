// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/base"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/catalog"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/profile"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/stage"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/version"
)

type CatalogSource interface {
	Fetch(context.Context, string) (*catalog.Index, error)
	LoadCached(context.Context, string) (*catalog.Index, error)
	Offline() bool
}

type CatalogState struct {
	Status  string         `json:"status"` // not-fetched, fetching, fresh, cached/offline, failed
	Channel string         `json:"channel"`
	Index   *catalog.Index `json:"index,omitempty"`
	Reason  string         `json:"reason,omitempty"`
	Updated time.Time      `json:"updated,omitempty"`
}

// ConfigureStorage is shell wiring. Tests can supply isolated directories and HTTP clients.
// Construction alone never fetches a catalog or opens hardware.
func (s *Service) ConfigureStorage(dataDir, cacheDir, backupDir string, source CatalogSource) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dataDir, s.cacheDir, s.backupDir = dataDir, cacheDir, backupDir
	if source == nil {
		source = catalog.NewSource(dataDir, version.Version, nil)
	}
	s.catalogSource = source
	s.catalogState = CatalogState{Status: "not-fetched", Channel: "beta"}
	s.artifacts = map[string]stage.ArtifactNeed{}
	s.picks = map[string]SelectedFile{}
	s.checkedFiles = map[string]bool{}
	// Stored paths are hints only. Prepare rechecks them before marking any row verified.
	if data, err := catalog.ReadFile(context.Background(), filepath.Join(dataDir, "selected-files.json"), catalog.MaxIndexSize); err == nil {
		var files []SelectedFile
		if json.Unmarshal(data, &files) == nil {
			for _, f := range files {
				if filepath.IsAbs(f.Path) && f.Size > 0 && len(f.SHA256) == 64 {
					s.picks[artifactKey(f.Layer, f.Role)] = f
				}
			}
		}
	}
}
func (s *Service) ConfigureDefaultStorage() error {
	data, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return err
	}
	backups, err := base.DefaultBackupDir()
	if err != nil {
		return err
	}
	s.ConfigureStorage(filepath.Join(data, "OpenXPlay Flasher"), filepath.Join(cache, "OpenXPlay Flasher"), backups, nil)
	return nil
}

func (s *Service) CatalogSnapshot() CatalogState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.catalogState
}
func (s *Service) CatalogFetch(ctx context.Context, channel string) (CatalogState, error) {
	s.catalogMu.Lock()
	defer s.catalogMu.Unlock()
	s.mu.Lock()
	source := s.catalogSource
	if channel == "" {
		channel = s.catalogState.Channel
	}
	if channel == "" {
		channel = "beta"
	}
	s.catalogState = CatalogState{Status: "fetching", Channel: channel}
	s.mu.Unlock()
	s.emitInstall()
	if source == nil {
		s.mu.Lock()
		s.catalogState = CatalogState{Status: "failed", Channel: channel, Reason: "catalog source unavailable / 目录来源不可用"}
		s.mu.Unlock()
		s.emitInstall()
		return s.CatalogSnapshot(), &stage.Problem{Code: "catalog-unavailable"}
	}
	i, err := source.Fetch(ctx, channel)
	state := CatalogState{Status: "fresh", Channel: channel, Index: i, Updated: time.Now().UTC()}
	if err != nil {
		i = nil
		state.Index = nil
		state.Status = "failed"
		state.Reason = err.Error()
	} else if source.Offline() {
		state.Status = "cached/offline"
	}
	s.mu.Lock()
	s.catalogState = state

	s.mu.Unlock()
	s.emitInstall()
	return state, err
}
func (s *Service) CatalogShow(ctx context.Context) (CatalogState, error) {
	s.catalogMu.Lock()
	defer s.catalogMu.Unlock()
	s.mu.Lock()
	source, channel := s.catalogSource, s.catalogState.Channel
	s.mu.Unlock()
	if source == nil {
		return s.CatalogSnapshot(), &stage.Problem{Code: "catalog-unavailable"}
	}
	if channel == "" {
		channel = "beta"
	}
	i, err := source.LoadCached(ctx, channel)
	state := CatalogState{Status: "cached/offline", Channel: channel, Index: i, Updated: time.Now().UTC()}
	if err != nil {
		i = nil
		state.Index = nil
		state.Status = "failed"
		state.Reason = err.Error()
	}
	s.mu.Lock()
	s.catalogState = state

	s.mu.Unlock()
	return state, err
}
func (s *Service) catalogLayerLocked(p profile.Profile, layer, version string) *catalog.Layer {
	if s.catalogState.Index == nil {
		return nil
	}
	dev, ok := s.catalogState.Index.Devices[p.ID]
	if !ok {
		return nil
	}
	if version == "" {
		version = dev.Recommended[layer]
	}
	if layer == "base" && version == "" && p.Base != nil {
		version = p.Base.Release
	}
	entry, ok := dev.Lookup(layer, version)
	if !ok {
		return nil
	}
	return &entry
}
func artifactKey(layer, role string) string { return layer + ":" + role }
func mapKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func (s *Service) BackupFolder(ctx context.Context, serial string) (string, error) {
	listing, err := s.BackupOperation(ctx, "show", serial, "")
	if err != nil {
		return "", err
	}
	folder := listing.(BackupListing).Folder
	st, err := os.Stat(folder)
	if err != nil || !st.IsDir() {
		return "", &stage.Problem{Code: "backup-folder"}
	}
	return folder, nil
}
