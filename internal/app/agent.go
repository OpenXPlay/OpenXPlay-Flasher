// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-22

package app

import (
	"context"
	"maps"
	"slices"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/catalog"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/device"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/profile"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/stage"
)

type StatusCandidate struct {
	device.Candidate
	Supported       bool `json:"supported"`
	StockDesignated bool `json:"stockDesignated"`
}

type StatusChoice struct {
	ID          string         `json:"id"`
	Destructive bool           `json:"destructive"`
	Available   bool           `json:"available"`
	Problem     *stage.Problem `json:"problem,omitempty"`
}

// DeviceStatus contains observations only, with no credential contents or package payloads.
type DeviceStatus struct {
	Candidates  []StatusCandidate `json:"candidates"`
	Target      *InstallTarget    `json:"target,omitempty"`
	Serial      string            `json:"serial,omitempty"`
	Installed   map[string]string `json:"installed"`
	Recommended map[string]string `json:"recommended"`
	Catalog     CatalogState      `json:"catalog"`
	Suggested   string            `json:"suggested"`
	Choices     []StatusChoice    `json:"choices"`
	Problem     *stage.Problem    `json:"problem,omitempty"`
}

// Status uses the same read-only inspection and suggestion as Connect in the desktop app.
// Unknown adb devices are described from transport metadata and are never opened here.
func (s *Service) Status(ctx context.Context) DeviceStatus {
	snap := s.Rescan(ctx)
	result := DeviceStatus{Candidates: []StatusCandidate{}, Installed: map[string]string{},
		Recommended: map[string]string{}, Catalog: s.CatalogSnapshot(), Choices: []StatusChoice{}}
	count := 0
	for _, c := range snap.Candidates {
		result.Candidates = append(result.Candidates, StatusCandidate{c, c.Supported(), c.Mode == profile.ModeStock})
		if c.Supported() || c.Mode == profile.ModeFastboot {
			count++
		}
	}
	switch {
	case snap.Error != "":
		result.Problem = &stage.Problem{Code: "usb", Detail: snap.Error}
		return result
	case count == 0:
		result.Problem = &stage.Problem{Code: "no-device"}
		return result
	case count > 1:
		result.Problem = &stage.Problem{Code: "several-devices"}
		return result
	}
	state := s.Install(ctx)
	result.Problem = state.Problem
	if state.InstalledStale {
		return result
	}
	result.Target, result.Suggested = state.Target, state.Suggested
	if state.Identity != nil {
		result.Serial = state.Identity.Serial
	}
	if state.Target != nil {
		for i := range result.Candidates {
			c := &result.Candidates[i]
			if c.Mode == state.Target.Device.Mode && c.USB.Location() == state.Target.Device.USB.Location() && c.Host == state.Target.Device.Host && c.Port == state.Target.Device.Port {
				c.Serialno = result.Serial
			}
		}
	}
	if state.Installed != nil {
		result.Installed = maps.Clone(state.Installed.Layers)
	}
	if state.Target != nil && result.Catalog.Verified && result.Catalog.Index != nil {
		if versions := result.Catalog.Index.Devices[state.Target.Profile].Recommended; versions != nil {
			result.Recommended = maps.Clone(versions)
		}
	}
	for _, c := range state.Choices {
		result.Choices = append(result.Choices, StatusChoice{c.ID, c.Destructive, c.Problem == nil, c.Problem})
	}
	return result
}

type ChoiceFile struct {
	Layer   string   `json:"layer"`
	Version string   `json:"version"`
	Role    string   `json:"role"`
	Name    string   `json:"name"`
	Size    int64    `json:"size"`
	SHA256  string   `json:"sha256"`
	URLs    []string `json:"urls"`
	Status  string   `json:"status"` // found or missing
	Path    string   `json:"path,omitempty"`
}

type ChoiceFiles struct {
	Choice   string       `json:"choice"`
	Profile  string       `json:"profile"`
	Files    []ChoiceFile `json:"files"`
	AllFound bool         `json:"allFound"`
}

// Files lists signed artifacts and rechecks only remembered package paths. It never fetches,
// searches directories, parses identity files, copies payloads, or changes the device.
func (s *Service) Files(ctx context.Context, choice string) (ChoiceFiles, error) {
	result := ChoiceFiles{Choice: choice, Files: []ChoiceFile{}}
	if len(choiceNeeds(choice)) == 0 {
		return result, &stage.Problem{Code: "bad-choice", Detail: choice}
	}
	if !s.configMu.TryLock() {
		return result, installProblem(ErrDeviceBusy)
	}
	defer s.configMu.Unlock()
	if _, err := s.inspectInstall(ctx, ""); err != nil {
		return result, installProblem(err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.installSnapshotLocked()
	if !state.Catalog.Verified || state.Catalog.Index == nil {
		return result, &stage.Problem{Code: "catalog-unavailable", Detail: "Run catalog fetch first / 请先运行 catalog fetch"}
	}
	if state.Target == nil {
		return result, ErrNoDevice
	}
	result.Profile = state.Target.Profile
	var p profile.Profile
	for _, candidate := range s.profiles {
		if candidate.ID == result.Profile {
			p = candidate
		}
	}
	var selected *stage.Choice
	for _, c := range state.Choices {
		if c.ID == choice {
			selected = &c
			break
		}
	}
	if selected == nil {
		return result, &stage.Problem{Code: "bad-choice", Detail: choice}
	}
	for _, need := range selected.Needs {
		// Follow the app's selected version when one is remembered, otherwise the recommendation.
		version := ""
		for _, key := range sortedKeys(s.picks) {
			picked := s.picks[key]
			if picked.Profile == p.ID && picked.Layer == need.Layer && picked.Version != "" {
				version = picked.Version
				break
			}
		}
		entry := s.catalogLayerLocked(p, need.Layer, version)
		if entry == nil && version != "" {
			entry = s.catalogLayerLocked(p, need.Layer, "")
		}
		if entry == nil {
			return result, &stage.Problem{Code: "catalog-layer", Detail: need.Layer}
		}
		artifacts := entry.Artifacts
		if need.Layer == "base" && p.Base != nil {
			artifacts = nil
			for _, role := range []string{"base-generic", "rootfs-zip", "boot"} {
				pin := p.Base.Archives[role]
				for _, a := range entry.Artifacts {
					if a.Role == role && a.Size == pin.Size && a.SHA256 == pin.SHA256 {
						artifacts = append(artifacts, a)
					}
				}
			}
			if len(artifacts) != 3 {
				return result, &stage.Problem{Code: "catalog-base", Detail: "Signed files do not match the profile pins / 签名文件与设备配置中的固定值不符"}
			}
		}
		for _, a := range artifacts {
			f := ChoiceFile{Layer: need.Layer, Version: entry.Version, Role: a.Role, Name: a.Name,
				Size: a.Size, SHA256: a.SHA256, URLs: slices.Clone(a.URLs), Status: "missing"}
			if picked, ok := s.picks[artifactKey(need.Layer, a.Role)]; ok && picked.Profile == p.ID && picked.Size == a.Size && picked.SHA256 == a.SHA256 {
				if err := catalog.VerifyContext(ctx, picked.Path, a); err == nil {
					f.Path, f.Status = picked.Path, "found"
				} else if ctx.Err() != nil {
					return result, ctx.Err()
				}
			}
			result.Files = append(result.Files, f)
		}
	}
	result.AllFound = len(result.Files) > 0
	for _, f := range result.Files {
		result.AllFound = result.AllFound && f.Status == "found"
	}
	return result, nil
}
