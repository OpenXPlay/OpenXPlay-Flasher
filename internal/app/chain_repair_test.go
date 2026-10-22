// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-22

package app

import (
	"bytes"
	"context"
	"io"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/base"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/profile"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/stage"
)

func interruptedBaseService(t *testing.T, original string) (*Service, *baseTestDevice, *baseTestFB, base.Journal, base.Backup) {
	t.Helper()
	s, d, fb, _, _ := baseServiceFixture(t)
	if _, err := s.CatalogFetch(t.Context(), "beta"); err != nil {
		t.Fatal(err)
	}
	s.Install(t.Context())
	selectFixtureFiles(t, s)
	if _, err := s.InstallPrepare(t.Context(), "full-reinstall"); err != nil {
		t.Fatal(err)
	}
	p := s.profiles[0]
	files := s.baseFilesLocked(p)
	if err := files.Prepare(t.Context()); err != nil {
		t.Fatal(err)
	}
	store := s.baseStore(p)
	store.Author = files.AuthorHashes()
	captured, err := store.Capture(t.Context(), "board1", "oem-dump", func(_ context.Context, name string, w io.Writer) error {
		v := byte(72)
		if name == "fsc" {
			v = 0
		}
		_, err := w.Write(bytes.Repeat([]byte{v}, 64))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now().Add(-time.Hour).UTC()
	info := d.det.Scan(t.Context()).Candidates[0].USB
	j := base.Journal{Schema: base.JournalSchema, Profile: p.ID, Serial: "board1", Plan: "stock", Choice: original, Started: started, Updated: started, CapturedSet: captured.Set, RestoreSet: captured.Set,
		Completed: []string{"chain-partition", "chain-hyp", "chain-rpm", "chain-sbl1"}, InFlight: "chain-tz", ChainConnection: &info}
	if err := (base.Journals{Dir: s.dataDir}).Save(t.Context(), j); err != nil {
		t.Fatal(err)
	}
	return s, d, fb, j, captured.Backup
}

func TestIncompleteChainPreviewAndRepairRetainOriginalScope(t *testing.T) {
	s, _, fb, previous, safe := interruptedBaseService(t, "stock")
	state := s.Install(t.Context())
	if state.Problem != nil || len(state.Choices) != 1 || state.Choices[0].ID != "repair" || state.Suggested != "repair" {
		t.Fatalf("state=%+v", state)
	}
	preview, err := s.InstallPlan(stage.Request{Choice: "repair"})
	if err != nil || !preview.Ready || preview.Steps[0].ID != "chain-tz" || !slices.Equal(preview.Layers, []string{"base", "device", "relay"}) {
		t.Fatalf("preview=%+v err=%v", preview, err)
	}
	expected := base.Steps(base.BuildRepairPlan(s.profiles[0], *state.Identity, &previous))
	for i, step := range expected {
		if !reflect.DeepEqual(preview.Steps[i], step) {
			t.Fatalf("preview[%d]=%+v want %+v", i, preview.Steps[i], step)
		}
	}
	if !strings.Contains(state.ChainNotice["en"], "Repair continues from chain-tz without restarting it") || !strings.Contains(state.ChainNotice["zh-CN"], "修复将从 chain-tz 继续") {
		t.Fatal("missing bilingual state", state.ChainNotice)
	}
	for _, step := range preview.Steps[:slices.IndexFunc(preview.Steps, func(s stage.Step) bool { return s.ID == "chain-cdt" })+1] {
		if step.Action == "backup" || step.Action == "erase" || strings.HasPrefix(step.Action, "reboot") {
			t.Fatalf("unsafe preview step %+v", step)
		}
	}
	fb.expectRestores = safe.Partitions
	if _, err := s.InstallStart(t.Context(), stage.Request{Choice: "repair", YesErase: "board1"}); err != nil {
		t.Fatal(err)
	}
	wait, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	state, err = s.InstallWait(wait)
	if err != nil || state.Job.Status != "succeeded" || state.Job.Installed.Layers["relay"] != "1.0~beta.1" || fb.calls[0] != "flash:tz" {
		t.Fatalf("job=%+v err=%v calls=%v", state.Job, err, fb.calls)
	}
	if slices.Contains(fb.calls, "oem dump fsc") || slices.Contains(fb.calls, "reboot-bootloader") || slices.Contains(fb.calls, "flash:partition") || len(fb.restored) != 4 {
		t.Fatal("unsafe repair", fb.calls)
	}
	journal, err := (base.Journals{Dir: s.dataDir}).Load(t.Context(), "ufi003", "board1")
	if err != nil || !journal.Finished || journal.RestoreSet != safe.Set || journal.Choice != "stock" {
		t.Fatal("lost original scope or set", journal, err)
	}
}

func TestIncompleteChainOffersNoRebootWhenSessionIsGone(t *testing.T) {
	for _, mode := range []string{"unplugged", "adb", "stock fastboot", "new session"} {
		t.Run(mode, func(t *testing.T) {
			s, d, fb, _, _ := interruptedBaseService(t, "repair")
			s.Install(t.Context())
			switch mode {
			case "unplugged":
				d.det.set()
			case "adb":
				d.det.set(testCandidate(profile.ModeADB, 1, true))
			case "stock fastboot":
				fb.product = "MSM8916"
			case "new session":
				c := d.det.Scan(t.Context()).Candidates[0]
				c.USB.Address++
				d.det.set(c)
			}
			state := s.Install(t.Context())
			if state.Problem == nil || state.Problem.Code != "chain-incomplete" || state.ChainNotice["en"] == "" || state.ChainNotice["zh-CN"] == "" {
				t.Fatalf("missing stopped state: %+v", state)
			}
			for _, c := range state.Choices {
				if c.Problem == nil || c.Ready || len(c.Steps) != 0 {
					t.Fatalf("offered a rebooting plan: %+v", c)
				}
			}
			if _, err := s.InstallStart(t.Context(), stage.Request{Choice: "repair", YesErase: "board1"}); err == nil || len(fb.calls) != 0 {
				t.Fatalf("repair changed missing session: %v calls=%v", err, fb.calls)
			}
		})
	}
}

func TestVerifiedSystemHistoryOffersNormalBaseChoices(t *testing.T) {
	for _, mode := range []profile.Mode{profile.ModeFastboot, profile.ModeADB} {
		t.Run(string(mode), func(t *testing.T) {
			s, d, fb, _, _ := baseServiceFixture(t)
			history, err := (base.Journals{Dir: "../base/testdata/chain-history"}).History(t.Context(), "ufi003", "4336bd41")
			if err != nil || len(history) != 2 {
				t.Fatalf("history=%+v err=%v", history, err)
			}
			serial := history[0].Serial
			fb.serial, d.serial = serial, serial
			d.det.set(testCandidate(mode, 1, true))
			if _, err := s.CatalogFetch(t.Context(), "beta"); err != nil {
				t.Fatal(err)
			}
			s.Install(t.Context())
			selectFixtureFiles(t, s)
			if _, err := s.InstallPrepare(t.Context(), "full-reinstall"); err != nil {
				t.Fatal(err)
			}
			before := s.Install(t.Context())
			journals := base.Journals{Dir: s.dataDir}
			for _, j := range history {
				if err := journals.Save(t.Context(), j); err != nil {
					t.Fatal(err)
				}
			}
			state := s.Install(t.Context())
			if state.Problem != nil || len(state.ChainNotice) != 0 || state.Resume == nil || !state.Resume.Finished || state.Resume.Plan != "system" {
				t.Fatalf("later Linux proof still blocks installation: %+v", state)
			}
			ids := func(choices []stage.Choice) []string {
				out := []string{}
				for _, c := range choices {
					out = append(out, c.ID)
				}
				return out
			}
			if !slices.Equal(ids(state.Choices), ids(before.Choices)) {
				t.Fatalf("history changed normal choices: got %v want %v", ids(state.Choices), ids(before.Choices))
			}
			for _, choice := range []string{"base", "full-reinstall"} {
				preview, err := s.InstallPlan(stage.Request{Choice: choice})
				if err != nil || !preview.Ready || preview.Problem != nil {
					t.Fatalf("%s preview=%+v err=%v", choice, preview, err)
				}
			}
			if mode == profile.ModeFastboot {
				preview, err := s.InstallPlan(stage.Request{Choice: "repair"})
				canonical := base.Steps(base.BuildPlan(s.profiles[0], "repair", *state.Identity))
				if err != nil || !preview.Ready || !reflect.DeepEqual(preview.Steps, canonical) || !slices.Equal(preview.Layers, []string{"base"}) {
					t.Fatalf("Repair should be canonical: %+v err=%v", preview, err)
				}
			}
			if len(fb.calls) != 0 {
				t.Fatal("planning changed the fake device", fb.calls)
			}
		})
	}
}
