// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package app

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/catalog"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/profile"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/stage"
)

func TestCatalogModeDefaultsAndSessionLog(t *testing.T) {
	s, _, _, source, _ := baseServiceFixture(t)
	if !s.Info().SignatureCheck || !s.CatalogSnapshot().SignatureCheck {
		t.Fatal("signature check must default to on")
	}
	for _, enabled := range []bool{true, false, true} {
		s.SetSignatureCheck(t.Context(), enabled)
		state, err := s.CatalogFetch(t.Context(), "beta")
		if err != nil || state.SignatureCheck != enabled || state.Verified != enabled || (source.mode == catalog.Verified) != enabled {
			t.Fatalf("mode %v: %+v %v", enabled, state, err)
		}
	}
	log := s.InstallLog()
	if strings.Count(log, "Package list signature check:") != 3 || strings.Count(log, "off /") != 1 {
		t.Fatal("mode was not logged once per fetch", log)
	}
}

func TestStartupRefreshesCachedListWithSignatureOff(t *testing.T) {
	s, _, _, source, _ := baseServiceFixture(t)
	s.SetSignatureCheck(t.Context(), false)
	if state := s.CatalogSnapshot(); state.Status != "cached/offline" || state.SignatureCheck {
		t.Fatal("cached mode was not applied", state)
	}
	s.Start(t.Context())
	s.Stop()
	if source.calls != 1 || source.mode != catalog.Unverified {
		t.Fatal("startup skipped the catalog fetch in off mode", source.calls, source.mode)
	}
}

func TestInstallStartRecordsSignatureMode(t *testing.T) {
	for _, choice := range []string{"base", "device"} {
		for _, enabled := range []bool{true, false} {
			t.Run(choice+map[bool]string{true: "-on", false: "-off"}[enabled], func(t *testing.T) {
				s, d, _, _, _ := baseServiceFixture(t)
				s.SetSignatureCheck(t.Context(), enabled)
				if _, err := s.CatalogFetch(t.Context(), "beta"); err != nil {
					t.Fatal(err)
				}
				if choice == "device" {
					d.det.set(testCandidate(profile.ModeADB, 1, true))
				}
				s.Install(t.Context())
				selectFixtureFiles(t, s)
				state, err := s.InstallStart(t.Context(), stage.Request{Choice: choice, YesErase: "board1"})
				mode := map[bool]string{true: "on", false: "off"}[enabled]
				text := "Install / 安装: Package list signature check: " + mode
				if err != nil || state.Job == nil || !strings.Contains(state.Job.Log, text) {
					t.Fatalf("start log: %+v %v", state.Job, err)
				}
				state, err = s.InstallWait(t.Context())
				if err != nil || state.Job.Status != "succeeded" || strings.Count(state.Job.Log, text) != 1 || s.InstallLog() != state.Job.Log {
					t.Fatalf("finished log: %+v %v", state.Job, err)
				}
			})
		}
	}
}

func TestSignatureOffStillEnforcesBasePinsAndFileHashes(t *testing.T) {
	s, _, _, source, payloads := baseServiceFixture(t)
	s.SetSignatureCheck(t.Context(), false)
	pin := s.profiles[0].Base.Archives["boot"]
	bad := append([]byte(nil), payloads[pin.File]...)
	bad[0] ^= 1
	device := source.index.Devices["ufi003"]
	for j := range device.Layers {
		if device.Layers[j].Layer == "base" {
			for k := range device.Layers[j].Artifacts {
				if device.Layers[j].Artifacts[k].Role == "boot" {
					device.Layers[j].Artifacts[k].SHA256 = testHash(bad)
				}
			}
		}
	}
	if _, err := s.CatalogFetch(t.Context(), "beta"); err != nil {
		t.Fatal(err)
	}
	s.Install(t.Context())
	filename := filepath.Join(t.TempDir(), "untrusted-boot.img")
	testPut(t, filename, bad)
	if _, err := s.ImportArtifact(t.Context(), "base", "boot", filename); err == nil {
		t.Fatal("catalog replaced a base profile pin")
	}
	testPut(t, filename, payloads[pin.File])
	if _, err := s.ImportArtifact(t.Context(), "base", "boot", filename); err != nil {
		t.Fatal("profile-pinned base file refused", err)
	}
	for name, data := range payloads {
		if !strings.HasSuffix(name, ".tar.gz") {
			continue
		}
		filename = filepath.Join(t.TempDir(), name)
		testPut(t, filename, append(data, 'x'))
		if _, err := s.ImportArtifact(t.Context(), "device", "bundle", filename); err == nil {
			t.Fatal("changed device bundle accepted with signature off")
		}
	}
}
