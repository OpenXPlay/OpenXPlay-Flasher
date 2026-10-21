// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
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
func (s *Service) ConfigureStorage(dataDir, cacheDir, backupDir string, source CatalogSource, cache *catalog.Cache) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dataDir, s.cacheDir, s.backupDir = dataDir, cacheDir, backupDir
	if source == nil {
		source = catalog.NewSource(dataDir, version.Version, nil)
	}
	if cache == nil {
		cache = catalog.NewCache(cacheDir, nil, nil)
	}
	cache.Version = version.Version
	s.catalogSource, s.catalogCache = source, cache
	s.catalogState = CatalogState{Status: "not-fetched", Channel: "beta"}
	s.artifacts = map[string]stage.ArtifactNeed{}
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
	s.ConfigureStorage(filepath.Join(data, "OpenXPlay Flasher"), filepath.Join(cache, "OpenXPlay Flasher"), backups, nil, nil)
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
	if source == nil {
		s.mu.Lock()
		s.catalogState = CatalogState{Status: "failed", Channel: channel, Reason: "catalog source unavailable / 目录来源不可用"}
		s.mu.Unlock()
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
	if s.catalogCache != nil {
		s.catalogCache.Index = i
	}
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
	if s.catalogCache != nil {
		s.catalogCache.Index = i
	}
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
func (s *Service) artifactUpdate(layer string, a catalog.Artifact, source string, p catalog.Progress, err error) {
	s.mu.Lock()
	if s.artifacts == nil {
		s.artifacts = map[string]stage.ArtifactNeed{}
	}
	if old, ok := s.artifacts[artifactKey(layer, a.Role)]; ok && old.SHA256 == a.SHA256 && old.Source == "imported" && source == "catalog" && p.URL == "" {
		source = "imported"
	}
	n := stage.ArtifactNeed{Role: a.Role, Name: a.Name, Size: a.Size, SHA256: a.SHA256, Source: source, Done: p.Done, Total: a.Size, Verified: err == nil && p.Done == a.Size && p.URL == ""}
	if err != nil {
		n.Error = &stage.Problem{Code: "download", Detail: err.Error()}
	}
	s.artifacts[artifactKey(layer, a.Role)] = n
	s.mu.Unlock()
	s.emitInstall()
}

// DownloadLayer resolves the recommended or explicitly selected catalog version. A local
// package remains the fallback if the catalog or a mirror is unavailable.
func (s *Service) DownloadLayer(ctx context.Context, profileID, layer, version string) (InstallState, error) {
	if !s.configMu.TryLock() {
		return s.InstallSnapshot(), ErrDeviceBusy
	}
	defer s.configMu.Unlock()
	err := s.downloadLayer(ctx, profileID, layer, version)
	return s.emitInstall(), err
}
func (s *Service) downloadLayer(ctx context.Context, profileID, layer, version string) error {
	s.catalogMu.Lock()
	defer s.catalogMu.Unlock()
	s.mu.Lock()
	var p profile.Profile
	for _, candidate := range s.profiles {
		if candidate.ID == profileID {
			p = candidate
		}
	}
	if layer == "base" && p.Base != nil {
		if version != "" && version != p.Base.Release {
			s.mu.Unlock()
			return &stage.Problem{Code: "base-version", Detail: version}
		}
		version = p.Base.Release
	}
	entry, cache := s.catalogLayerLocked(p, layer, version), s.catalogCache
	s.mu.Unlock()
	if p.ID == "" || layer != "base" && layer != "device" && layer != "relay" {
		return &stage.Problem{Code: "wrong-layer", Detail: layer}
	}
	if layer == "base" {
		provider := base.Provider{Cache: cache, Layer: entry, Dir: s.cacheDir}
		if p.Base == nil {
			return &stage.Problem{Code: "base-unavailable"}
		}
		roles := mapKeys(p.Base.Archives)
		slices.Sort(roles)
		for _, role := range roles {
			pin := p.Base.Archives[role]
			a := catalog.Artifact{Role: role, Name: pin.File, Size: pin.Size, SHA256: pin.SHA256}
			path, source, err := provider.Resolve(ctx, role, pin, func(progress catalog.Progress) { s.artifactUpdate(layer, a, "catalog", progress, nil) })
			_ = path
			s.artifactUpdate(layer, a, source, catalog.Progress{Done: pin.Size, Total: pin.Size}, err)
			if err != nil {
				return err
			}
		}
		return nil
	}
	if entry == nil || cache == nil {
		return &stage.Problem{Code: "catalog-unavailable", Detail: layer}
	}
	paths := map[string]string{}
	for _, a := range entry.Artifacts {
		path, err := cache.Ensure(ctx, a, func(p catalog.Progress) { s.artifactUpdate(layer, a, "catalog", p, nil) })
		s.artifactUpdate(layer, a, "catalog", catalog.Progress{Done: a.Size, Total: a.Size}, err)
		if err != nil {
			return err
		}
		paths[a.Role] = path
	}
	var pkg *stage.Package
	var err error
	if layer == "device" {
		for _, a := range entry.Artifacts {
			if strings.HasSuffix(a.Name, ".tar.gz") {
				pkg, err = stage.Open(paths[a.Role])
				break
			}
		}
	} else {
		var metadata, data []byte
		for _, a := range entry.Artifacts {
			if strings.HasSuffix(a.Name, ".manifest.json") {
				metadata, err = catalog.ReadFile(ctx, paths[a.Role], 1<<20)
			}
			if strings.HasSuffix(a.Name, ".deb") {
				data, err = catalog.ReadFile(ctx, paths[a.Role], stage.MaxPackage)
			}
			if err != nil {
				return err
			}
		}
		pkg, err = stage.RelayPackage(metadata, data)
	}
	if err != nil {
		return err
	}
	if pkg == nil {
		return &stage.Problem{Code: "no-package", Detail: layer}
	}
	m := pkg.Manifest()
	if m.Stage != layer || m.Device != "" && m.Device != p.ID || m.Version != entry.Version {
		return &stage.Problem{Code: "catalog-package", Detail: layer}
	}
	s.mu.Lock()
	s.packages = slices.DeleteFunc(s.packages, func(p *stage.Package) bool { return p.Manifest().Stage == layer })
	s.packages = append(s.packages, pkg)
	delete(s.packageProblems, layer)
	s.mu.Unlock()
	return nil
}
func mapKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// ImportFile first uses the signed index. Base archive pins provide an offline fallback even
// without any catalog; ordinary stage packages retain their existing local-file checks.
func (s *Service) ImportFile(ctx context.Context, path string) (InstallState, error) {
	if !s.configMu.TryLock() {
		return s.InstallSnapshot(), ErrDeviceBusy
	}
	defer s.configMu.Unlock()
	err := s.importFile(ctx, path)
	return s.emitInstall(), err
}
func (s *Service) importFile(ctx context.Context, path string) error {
	s.catalogMu.Lock()
	defer s.catalogMu.Unlock()
	s.mu.Lock()
	cache := s.catalogCache
	profiles := s.profiles
	s.mu.Unlock()
	matched := false
	if cache != nil && cache.Index != nil {
		dest, err := cache.Import(ctx, path)
		if err == nil {
			matched = true
			for id, d := range cache.Index.Devices {
				_ = id
				for _, l := range d.Layers {
					for _, a := range l.Artifacts {
						if filepath.Base(dest) == a.Name && filepath.Base(filepath.Dir(dest)) == a.SHA256 {
							s.artifactUpdate(l.Layer, a, "imported", catalog.Progress{Done: a.Size, Total: a.Size}, nil)
						}
					}
				}
			}
		} else if !errors.Is(err, catalog.ErrNoMatch) {
			return err
		}
	}
	for _, p := range profiles {
		provider := base.Provider{Dir: s.cacheDir}
		if _, err := provider.Import(ctx, p.Base, path); err == nil {
			n, sum, _ := catalog.Hash(ctx, path)
			for role, pin := range p.Base.Archives {
				if pin.Size == n && pin.SHA256 == sum {
					s.artifactUpdate("base", catalog.Artifact{Role: role, Name: pin.File, Size: pin.Size, SHA256: pin.SHA256}, "imported", catalog.Progress{Done: pin.Size, Total: pin.Size}, nil)
				}
			}
			return nil
		} else if !errors.Is(err, catalog.ErrNoMatch) {
			return err
		}
	}
	if matched {
		return nil
	}
	pkg, err := stage.Open(path)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.packages = slices.DeleteFunc(s.packages, func(p *stage.Package) bool { return p.Manifest().Stage == pkg.Manifest().Stage })
	s.packages = append(s.packages, pkg)
	s.recordLocalPackageLocked(pkg)
	s.mu.Unlock()
	return nil
}

// Prepare obtains every need for a choice before any device write. It is separate from start
// so a GUI can show download progress and keep start's HTTP request short.
func (s *Service) InstallPrepare(ctx context.Context, choice string) (InstallState, error) {
	if !s.configMu.TryLock() {
		return s.InstallSnapshot(), ErrDeviceBusy
	}
	defer s.configMu.Unlock()
	s.mu.Lock()
	target := s.installTarget
	s.mu.Unlock()
	if target == nil {
		return s.InstallSnapshot(), ErrNoDevice
	}
	needs := choiceNeeds(choice)
	if len(needs) == 0 {
		return s.InstallSnapshot(), &stage.Problem{Code: "bad-choice", Detail: choice}
	}
	var failures []error
	for _, layer := range needs {
		err := s.downloadLayer(ctx, target.Profile, layer, "")
		if err != nil {
			if layer == "base" {
				failures = append(failures, err)
				continue
			}
			s.mu.Lock()
			pkg, problem := stage.PackageSet(s.packages).ForLayer(layer)
			s.mu.Unlock()
			if pkg == nil || problem != nil {
				failures = append(failures, err)
			}
		}
	}
	return s.emitInstall(), errors.Join(failures...)
}

func (s *Service) recordLocalPackageLocked(pkg *stage.Package) {
	if s.artifacts == nil {
		s.artifacts = map[string]stage.ArtifactNeed{}
	}
	info, m := pkg.Info(), pkg.Manifest()
	name := m.File
	if name == "" {
		name = m.Name + ".tar.gz"
	}
	s.artifacts[artifactKey(m.Stage, "local")] = stage.ArtifactNeed{Role: "local", Name: name, Size: info.Size, SHA256: info.SHA256, Source: "local", Done: info.Size, Total: info.Size, Verified: true}
}
