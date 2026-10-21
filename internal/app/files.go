// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/catalog"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/profile"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/stage"
)

// SelectedFile is metadata only. Picked files stay at their original paths.
type SelectedFile struct {
	Path    string `json:"path"`
	Size    int64  `json:"size"`
	SHA256  string `json:"sha256"`
	Profile string `json:"profile"`
	Layer   string `json:"layer"`
	Role    string `json:"role"`
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}
type FileCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Done   int64  `json:"done"`
	Total  int64  `json:"total"`
}
type FileResult struct {
	Name    string         `json:"name"`
	Size    int64          `json:"size"`
	SHA256  string         `json:"sha256"`
	Layer   string         `json:"layer,omitempty"`
	Role    string         `json:"role,omitempty"`
	Problem *stage.Problem `json:"problem,omitempty"`
}
type hashedFile struct {
	SelectedFile
	data    []byte
	problem *stage.Problem
}

func fileDetail(f SelectedFile) string {
	return fmt.Sprintf("%s · %d B · SHA-256 %s", filepath.Base(f.Path), f.Size, f.SHA256[:min(12, len(f.SHA256))])
}
func (s *Service) saveSelections(ctx context.Context) error {
	s.mu.Lock()
	dir := s.dataDir
	files := make([]SelectedFile, 0, len(s.picks))
	for _, key := range sortedKeys(s.picks) {
		files = append(files, s.picks[key])
	}
	s.mu.Unlock()
	if dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(files, "", "  ")
	if err != nil {
		return err
	}
	return catalog.WriteFile(ctx, filepath.Join(dir, "selected-files.json"), data, 0o600)
}

func (s *Service) CancelFileCheck() InstallState {
	s.mu.Lock()
	if s.checkCancel != nil {
		s.checkCancel()
	}
	s.mu.Unlock()
	return s.InstallSnapshot()
}

// SelectFiles hashes the complete selection before assigning by content. It never fetches or
// copies a package. Per-file results let a good selection survive an unrelated unmatched file.
func (s *Service) SelectFiles(ctx context.Context, choice, layer, role string, paths []string) (InstallState, error) {
	if !s.configMu.TryLock() {
		return s.InstallSnapshot(), ErrDeviceBusy
	}
	defer s.configMu.Unlock()
	s.catalogMu.Lock()
	defer s.catalogMu.Unlock()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	s.mu.Lock()
	s.checkCancel = cancel
	s.fileResults = []FileResult{}
	if s.picks == nil {
		s.picks = map[string]SelectedFile{}
	}
	if s.checkedFiles == nil {
		s.checkedFiles = map[string]bool{}
	}
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.checkCancel = nil; s.mu.Unlock() }()
	files := make([]hashedFile, 0, len(paths))

	for _, filename := range paths {
		files = append(files, s.hashSelected(ctx, filename))
		if ctx.Err() != nil {
			break
		}
	}
	// The compatibility single-file importer still recognizes a relay's adjacent sidecar.
	// Even this path hashes payloads with progress and cancellation, under the operation lock.
	if len(files) == 1 && files[0].problem == nil && ctx.Err() == nil {
		first := files[0]
		dir := filepath.Dir(first.Path)
		if strings.HasSuffix(first.Path, ".deb") {
			candidates, _ := filepath.Glob(filepath.Join(dir, "*.manifest.json"))
			for _, filename := range candidates {
				metadata, err := catalog.ReadFile(ctx, filename, 1<<20)
				if err != nil {
					continue
				}
				if m, err := stage.RelayManifest(metadata); err == nil && strings.EqualFold(m.SHA256, first.SHA256) {
					files = append(files, s.hashSelected(ctx, filename))
					role = ""
					break
				}
			}
		} else if strings.HasSuffix(first.Path, ".manifest.json") {
			if m, err := stage.RelayManifest(first.data); err == nil {
				others, _ := filepath.Glob(filepath.Join(dir, "*.deb"))
				candidates := append([]string{filepath.Join(dir, m.File), filepath.Join(dir, strings.ReplaceAll(m.File, "~", "-"))}, others...)
				seen := map[string]bool{}
				for _, filename := range candidates {
					if seen[filename] {
						continue
					}
					seen[filename] = true
					candidate := s.hashSelected(ctx, filename)
					if ctx.Err() != nil {
						files = append(files, candidate)
						break
					}
					if candidate.problem == nil && strings.EqualFold(candidate.SHA256, m.SHA256) {
						files = append(files, candidate)
						role = ""
						break
					}
				}
			}
		}
	}

	for i := range files {
		f := &files[i]
		if f.problem == nil {
			s.assignFile(f, files, choice, layer, role)
		}
		s.mu.Lock()
		s.fileResults = append(s.fileResults, FileResult{Name: filepath.Base(f.Path), Size: f.Size, SHA256: f.SHA256, Layer: f.Layer, Role: f.Role, Problem: f.problem})
		s.mu.Unlock()
	}
	var storageErr error
	affected := []string{}
	for _, f := range files {
		if f.problem == nil && f.Layer != "" && !slices.Contains(affected, f.Layer) {
			affected = append(affected, f.Layer)
		}
	}
	if ctx.Err() == nil {
		if err := s.rebuildPackages(ctx, affected...); err != nil {
			s.mu.Lock()
			problem := installProblem(err)
			if errors.Is(err, stage.ErrPackage) {
				problem = &stage.Problem{Code: "package", Detail: err.Error()}
			}
			s.fileResults = append(s.fileResults, FileResult{Problem: problem})
			s.mu.Unlock()
		}
		if err := s.saveSelections(ctx); err != nil {
			storageErr = err
		}
	}
	s.mu.Lock()
	status := "complete"
	if ctx.Err() != nil {
		status = "cancelled"
	}
	if s.fileCheck != nil {
		c := *s.fileCheck
		c.Status = status
		s.fileCheck = &c
	}
	s.mu.Unlock()
	return s.emitInstall(), storageErr
}

func (s *Service) assignFile(f *hashedFile, batch []hashedFile, choice, layer, role string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	layers := choiceNeeds(choice)
	allowed := func(l, r string) bool {
		return (layer == "" || layer == l) && (role == "" || role == r) && (len(layers) == 0 || slices.Contains(layers, l))
	}
	profiles := s.profiles
	if s.installTarget != nil {
		profiles = slices.DeleteFunc(slices.Clone(profiles), func(p profile.Profile) bool { return p.ID != s.installTarget.Profile })
	}
	for _, p := range profiles {
		if p.Base != nil {
			for r, a := range p.Base.Archives {
				if allowed("base", r) && a.Size == f.Size && a.SHA256 == f.SHA256 {
					f.Profile, f.Layer, f.Role, f.Name, f.Version = p.ID, "base", r, a.File, p.Base.Release
					s.rememberFileLocked(f.SelectedFile)
					return
				}
			}
		}
		if s.catalogState.Index != nil {
			for _, entry := range s.catalogState.Index.Devices[p.ID].Layers {
				if entry.Layer == "base" {
					continue
				} // Profile pins are the authority for the base.
				for _, a := range entry.Artifacts {
					if allowed(entry.Layer, a.Role) && a.Size == f.Size && a.SHA256 == f.SHA256 {
						f.Profile, f.Layer, f.Role, f.Name, f.Version = p.ID, entry.Layer, a.Role, a.Name, entry.Version
						s.rememberFileLocked(f.SelectedFile)
						return
					}
				}
			}
		}
	}
	// The existing stage parsers supply the offline manifest checks. Filenames never classify
	// valid input; the extension is used only to make a refusal more specific.
	var detected, fileRole, name, version string
	if len(f.data) > 1 && f.data[0] == 0x1f && f.data[1] == 0x8b {
		if pkg, err := stage.DevicePackage(f.data); err == nil {
			m := pkg.Manifest()
			detected, fileRole, name, version = "device", "bundle", m.Name+".tar.gz", m.Version
			f.Profile = m.Device
		}
	} else if m, err := stage.RelayManifest(f.data); err == nil {
		detected, fileRole, name, version = "relay", "manifest", "relay.manifest.json", m.Version
	} else {
		for _, other := range batch {
			if m, err := stage.RelayManifest(other.data); err == nil && strings.EqualFold(m.SHA256, f.SHA256) {
				detected, fileRole, name, version = "relay", "deb", m.File, m.Version
			}
		}
		for _, picked := range s.picks {
			if picked.Layer == "relay" && strings.HasSuffix(picked.Name, ".manifest.json") {
				data, err := catalog.ReadFile(context.Background(), picked.Path, 1<<20)
				if err == nil {
					if m, err := stage.RelayManifest(data); err == nil && strings.EqualFold(m.SHA256, f.SHA256) {
						detected, fileRole, name, version = "relay", "deb", m.File, m.Version
					}
				}
			}
		}
		if detected == "" && bytes.HasPrefix(f.data, []byte("!<arch>\n")) {
			detected, fileRole, name = "relay", "deb", "relay.deb"
		}
	}
	if detected != "" && layer != "" && detected != layer && role == "" {
		f.problem = &stage.Problem{Code: "wrong-layer", Detail: layer}
		return
	}
	if s.catalogState.Index != nil {
		if detected == "" && strings.HasSuffix(strings.ToLower(f.Path), ".deb") {
			detected, fileRole = "relay", "deb"
		}
		if detected != "" && allowed(detected, fileRole) {
			f.problem = s.unknownPackageLocked(detected)
			return
		}
	} else if detected != "" && allowed(detected, fileRole) {
		if f.Profile == "" && len(profiles) > 0 {
			f.Profile = profiles[0].ID
		}
		if len(profiles) > 0 && f.Profile != profiles[0].ID {
			f.problem = &stage.Problem{Code: "wrong-device", Detail: f.Profile}
			return
		}
		f.Layer, f.Role, f.Name, f.Version = detected, fileRole, name, version
		s.rememberFileLocked(f.SelectedFile)
		return
	}
	f.problem = &stage.Problem{Code: "unmatched-file", Detail: fileDetail(f.SelectedFile)}
}
func (s *Service) rememberFileLocked(f SelectedFile) {
	s.packages = slices.DeleteFunc(s.packages, func(p *stage.Package) bool { return p.Manifest().Stage == f.Layer })
	for key, old := range s.picks {
		if old.Layer == f.Layer && (old.Profile != f.Profile || old.Version != "" && f.Version != "" && old.Version != f.Version) {
			delete(s.picks, key)
		}
	}
	s.picks[artifactKey(f.Layer, f.Role)] = f
	s.checkedFiles[f.SHA256] = true
	if s.artifacts == nil {
		s.artifacts = map[string]stage.ArtifactNeed{}
	}
	s.artifacts[artifactKey(f.Layer, f.Role)] = stage.ArtifactNeed{Role: f.Role, Name: f.Name, Path: f.Path, Size: f.Size, SHA256: f.SHA256, Source: "selected", Done: f.Size, Total: f.Size, Verified: true, Status: "verified"}
}
func (s *Service) unknownPackageLocked(layer string) *stage.Problem {
	versions := []string{}
	if s.catalogState.Index != nil {
		for id, d := range s.catalogState.Index.Devices {
			if s.installTarget != nil && s.installTarget.Profile != id {
				continue
			}
			for _, l := range d.Layers {
				if l.Layer == layer {
					versions = append(versions, l.Version)
				}
			}
		}
	}
	slices.Sort(versions)
	versions = slices.Compact(versions)
	if len(versions) == 0 {
		return &stage.Problem{Code: "catalog-no-versions", Detail: layer}
	}
	return &stage.Problem{Code: "catalog-unknown", Detail: strings.Join(versions, ", ")}
}

func (s *Service) rebuildPackages(ctx context.Context, layers ...string) error {
	s.mu.Lock()
	picks := make(map[string]SelectedFile, len(s.picks))
	for key, f := range s.picks {
		if slices.Contains(layers, f.Layer) {
			picks[key] = f
		}
	}
	s.mu.Unlock()
	read := func(f SelectedFile) ([]byte, error) {
		data, err := catalog.ReadFile(ctx, f.Path, stage.MaxPackage)
		if err != nil || int64(len(data)) != f.Size || hashBytes(data) != f.SHA256 {
			return nil, &stage.Problem{Code: "file-changed", Detail: filepath.Base(f.Path)}
		}
		return data, nil
	}
	var device, metadata, payload *SelectedFile
	for _, f := range picks {
		f := f
		if f.Layer == "device" {
			device = &f
		}
		if f.Layer == "relay" {
			if strings.HasSuffix(f.Name, ".manifest.json") {
				metadata = &f
			} else {
				payload = &f
			}
		}
	}
	add := func(pkg *stage.Package, files ...SelectedFile) error {
		sources := []stage.LocalFile{}
		for _, f := range files {
			sources = append(sources, stage.LocalFile{Path: f.Path, Size: f.Size, SHA256: f.SHA256})
		}
		pkg.SetLocalFiles(sources)
		s.mu.Lock()
		defer s.mu.Unlock()
		if problem := s.catalogPackageProblemLocked(pkg); problem != nil {
			return problem
		}
		s.packages = slices.DeleteFunc(s.packages, func(p *stage.Package) bool { return p.Manifest().Stage == pkg.Manifest().Stage })
		s.packages = append(s.packages, pkg)
		delete(s.packageProblems, pkg.Manifest().Stage)
		return nil
	}
	if device != nil {
		data, err := read(*device)
		if err != nil {
			return err
		}
		pkg, err := stage.DevicePackage(data)
		if err != nil {
			return err
		}
		if err = add(pkg, *device); err != nil {
			return err
		}
	}
	if metadata != nil && payload != nil {
		m, err := read(*metadata)
		if err != nil {
			return err
		}
		data, err := read(*payload)
		if err != nil {
			return err
		}
		pkg, err := stage.RelayPackage(m, data)
		if err != nil {
			return err
		}
		if err = add(pkg, *metadata, *payload); err != nil {
			return err
		}
	}
	return nil
}
func (s *Service) catalogPackageProblemLocked(pkg *stage.Package) *stage.Problem {
	if s.catalogState.Index == nil {
		return nil
	}
	m := pkg.Manifest()
	for id, d := range s.catalogState.Index.Devices {
		if s.installTarget != nil && id != s.installTarget.Profile || m.Device != "" && id != m.Device {
			continue
		}
		for _, l := range d.Layers {
			if l.Layer != m.Stage || l.Version != m.Version {
				continue
			}
			matched := 0
			for _, source := range pkg.LocalFiles() {
				for _, a := range l.Artifacts {
					if a.Size == source.Size && a.SHA256 == source.SHA256 {
						matched++
						break
					}
				}
			}
			if matched > 0 && matched == len(pkg.LocalFiles()) && matched == len(l.Artifacts) {
				return nil
			}
		}
	}
	return s.unknownPackageLocked(m.Stage)
}

func (s *Service) selectOne(ctx context.Context, choice, layer, role, path string) (InstallState, error) {
	state, err := s.SelectFiles(ctx, choice, layer, role, []string{path})
	if err == nil {
		for _, result := range state.FileResults {
			if result.Problem != nil {
				err = result.Problem
				break
			}
		}
	}
	return state, err
}
func (s *Service) ImportFile(ctx context.Context, path string) (InstallState, error) {
	return s.selectOne(ctx, "", "", "", path)
}
func (s *Service) ImportArtifact(ctx context.Context, layer, role, path string) (InstallState, error) {
	return s.selectOne(ctx, "", layer, role, path)
}

// InstallPrepare restores and rechecks remembered paths. An empty row stays empty until the
// user selects its file; entering Prepare never performs network I/O.
func (s *Service) InstallPrepare(ctx context.Context, choice string) (InstallState, error) {
	if len(choiceNeeds(choice)) == 0 {
		return s.InstallSnapshot(), &stage.Problem{Code: "bad-choice", Detail: choice}
	}
	s.mu.Lock()
	paths := []string{}
	for _, key := range sortedKeys(s.picks) {
		f := s.picks[key]
		if slices.Contains(choiceNeeds(choice), f.Layer) && !s.checkedFiles[f.SHA256] {
			paths = append(paths, f.Path)
		}
	}
	s.mu.Unlock()
	if len(paths) > 0 {
		return s.SelectFiles(ctx, choice, "", "", paths)
	}
	if !s.configMu.TryLock() {
		return s.InstallSnapshot(), ErrDeviceBusy
	}
	defer s.configMu.Unlock()
	err := s.rebuildPackages(ctx, choiceNeeds(choice)...)
	return s.emitInstall(), err
}

func hashBytes(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }

func (s *Service) hashSelected(ctx context.Context, filename string) hashedFile {
	filename, err := filepath.Abs(filename)
	f := hashedFile{SelectedFile: SelectedFile{Path: filename}}
	if err != nil {
		f.problem = &stage.Problem{Code: "file-read", Detail: filepath.Base(filename)}
	} else {
		last := time.Time{}
		n, sum, hashErr := catalog.HashProgress(ctx, filename, func(p catalog.Progress) {
			if p.Done != p.Total && time.Since(last) < 100*time.Millisecond {
				return
			}
			last = time.Now()
			s.mu.Lock()
			s.fileCheck = &FileCheck{Name: filepath.Base(filename), Status: "checking", Done: p.Done, Total: p.Total}
			s.mu.Unlock()
			s.emitInstall()
		})
		f.Size, f.SHA256 = n, sum
		if hashErr != nil {
			code := "file-read"
			if errors.Is(hashErr, context.Canceled) || errors.Is(hashErr, context.DeadlineExceeded) {
				code = "file-cancelled"
			}
			f.problem = &stage.Problem{Code: code, Detail: filepath.Base(filename)}
		} else if n <= stage.MaxPackage {
			f.data, err = catalog.ReadFile(ctx, filename, stage.MaxPackage)
			// Bind parsing to the same bytes that were just hashed, even if the file changes now.
			if err != nil || int64(len(f.data)) != n || hashBytes(f.data) != sum {
				code := "file-changed"
				if ctx.Err() != nil {
					code = "file-cancelled"
				}
				f.problem = &stage.Problem{Code: code, Detail: filepath.Base(filename)}
			}
		}
	}
	return f
}
