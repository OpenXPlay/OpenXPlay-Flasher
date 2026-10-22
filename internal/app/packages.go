// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/base"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/catalog"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/privatefile"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/profile"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/stage"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/version"
)

type CatalogSource interface {
	Fetch(context.Context, string, catalog.Mode) (*catalog.Result, error)
	LoadCached(context.Context, string, catalog.Mode) (*catalog.Result, error)
	Offline() bool
}

type CatalogState struct {
	Status         string         `json:"status"` // not-fetched, fetching, fresh, cached/offline, failed
	Channel        string         `json:"channel"`
	Index          *catalog.Index `json:"index,omitempty"`
	Reason         string         `json:"reason,omitempty"`
	SignatureCheck bool           `json:"signatureCheck"`
	Verified       bool           `json:"verified"`
	Updated        time.Time      `json:"updated,omitempty"`
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
	s.catalogFetched = false
	s.catalogState = CatalogState{Status: "not-fetched", Channel: "beta", SignatureCheck: !s.noSignatureCheck}
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
	data = filepath.Join(data, "OpenXPlay Flasher")
	if err := privatefile.Dir(data); err != nil {
		return err
	}
	s.ConfigureStorage(data, filepath.Join(cache, "OpenXPlay Flasher"), backups, nil)
	return nil
}

func (s *Service) CatalogSnapshot() CatalogState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.catalogState
}

func (s *Service) catalogModeLocked() catalog.Mode {
	if s.noSignatureCheck {
		return catalog.Unverified
	}
	return catalog.Verified
}

// SetSignatureCheck immediately replaces the active list with this mode's cached copy. A
// missing copy leaves no active index; the next fetch still follows the newly selected mode.
func (s *Service) SetSignatureCheck(ctx context.Context, enabled bool) {
	s.catalogMu.Lock()
	defer s.catalogMu.Unlock()
	s.mu.Lock()
	if s.noSignatureCheck == !enabled {
		s.mu.Unlock()
		return
	}
	s.noSignatureCheck = !enabled
	source, channel, mode := s.catalogSource, s.catalogState.Channel, s.catalogModeLocked()
	if channel == "" {
		channel = "beta"
	}
	s.catalogState = CatalogState{Status: "not-fetched", Channel: channel, SignatureCheck: enabled}
	s.mu.Unlock()
	if source != nil {
		if result, err := source.LoadCached(ctx, channel, mode); err == nil {
			s.mu.Lock()
			s.catalogState = catalogResult(result, channel, enabled, "cached/offline", nil)
			s.mu.Unlock()
		}
	}
	s.emitInstall()
}

func catalogResult(result *catalog.Result, channel string, enabled bool, status string, err error) CatalogState {
	state := CatalogState{Status: status, Channel: channel, SignatureCheck: enabled, Updated: time.Now().UTC()}
	if err != nil {
		state.Status, state.Reason = "failed", err.Error()
	} else if result != nil {
		state.Index, state.Verified, state.Reason = result.Index, result.Verified, result.Reason
	}
	return state
}

func (s *Service) signatureLogLocked() string {
	if s.noSignatureCheck {
		return "Package list signature check: off / 软件包列表签名校验：已关闭\n"
	}
	return "Package list signature check: on / 软件包列表签名校验：已开启\n"
}

func (s *Service) CatalogFetch(ctx context.Context, channel string) (CatalogState, error) {
	s.catalogMu.Lock()
	defer s.catalogMu.Unlock()
	s.mu.Lock()
	source := s.catalogSource
	s.catalogFetched = true
	mode, enabled := s.catalogModeLocked(), !s.noSignatureCheck
	if channel == "" {
		channel = s.catalogState.Channel
	}
	if channel == "" {
		channel = "beta"
	}
	s.catalogLog += fmt.Sprintf("[%s] %s", time.Now().UTC().Format(time.RFC3339), s.signatureLogLocked())
	s.catalogState = CatalogState{Status: "fetching", Channel: channel, SignatureCheck: enabled}
	s.mu.Unlock()
	s.emitInstall()
	if source == nil {
		s.mu.Lock()
		s.catalogState = catalogResult(nil, channel, enabled, "failed", &stage.Problem{Code: "catalog-unavailable", Detail: "catalog source unavailable / 软件包列表来源不可用"})
		s.mu.Unlock()
		s.emitInstall()
		return s.CatalogSnapshot(), &stage.Problem{Code: "catalog-unavailable"}
	}
	result, err := source.Fetch(ctx, channel, mode)
	state := catalogResult(result, channel, enabled, "fresh", err)
	if err == nil && source.Offline() {
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
	s.catalogFetched = true
	mode, enabled := s.catalogModeLocked(), !s.noSignatureCheck
	s.mu.Unlock()
	if source == nil {
		return s.CatalogSnapshot(), &stage.Problem{Code: "catalog-unavailable"}
	}
	if channel == "" {
		channel = "beta"
	}
	result, err := source.LoadCached(ctx, channel, mode)
	state := catalogResult(result, channel, enabled, "cached/offline", err)
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
