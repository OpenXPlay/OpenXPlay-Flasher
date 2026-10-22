// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-22

package base

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/profile"
)

func chainHistory(t *testing.T) (Journal, Journal) {
	t.Helper()
	history, err := (Journals{Dir: "testdata/chain-history"}).History(t.Context(), "ufi003", "4336bd41")
	if err != nil || len(history) != 2 {
		t.Fatalf("history=%+v err=%v", history, err)
	}
	return history[1], history[0]
}

func TestSystemProofSupersedesIncompleteChainWithoutReplacingRestoreSource(t *testing.T) {
	for _, later := range []bool{false, true} {
		t.Run(map[bool]string{false: "older only", true: "verified system job"}[later], func(t *testing.T) {
			older, newer := chainHistory(t)
			e, _, fb, _ := executorFixture(t, "repair", profile.ModeFastboot, "LK1ST_MSM8916")
			e.Store.Now = func() time.Time { return older.CompletedAt["backup"] }
			safe := fb.f.capture(t, older.Serial)
			if safe.Set != older.RepairSet() {
				t.Fatalf("fixture backup=%s want=%s", safe.Set, older.RepairSet())
			}
			if err := e.Journals.Save(t.Context(), older); err != nil {
				t.Fatal(err)
			}
			if later {
				if err := e.Journals.Save(t.Context(), newer); err != nil {
					t.Fatal(err)
				}
			}
			source, err := e.Journals.RepairSource(t.Context(), older.Profile, older.Serial)
			if err != nil || source == nil || IncompleteChain(source) == later || source.RepairSet() != older.RepairSet() {
				t.Fatalf("source=%+v err=%v", source, err)
			}
			// Supersession is derived state: the original journal and restore set stay intact.
			got, _ := json.Marshal(source)
			want, _ := json.Marshal(older)
			if string(got) != string(want) {
				t.Fatalf("changed source journal: %s", got)
			}
			start := Identity{Mode: profile.ModeFastboot, Product: e.Profile.Base.Fastboot.Product, Serial: older.Serial}
			plan := BuildRepairPlan(e.Profile, start, source)
			if later {
				if !reflect.DeepEqual(plan, BuildPlan(e.Profile, "repair", start)) {
					t.Fatal("later Linux proof still offered a chain suffix", plan)
				}
			} else if plan.Problem != nil || plan.Steps[0].ID != "chain-tz" {
				t.Fatal("older journal lost its continuation", plan)
			}
			fb.serial = older.Serial
			e.ADB.(*fakeConnector).d.serial = older.Serial
			fb.expectRestores = safe.Partitions
			fb.f.dumps["modemst1"][0]++
			job := e.Run(t.Context(), plan)
			if job.Status != "succeeded" || e.Journal.RestoreSet != safe.Set || !slices.Equal(fb.restored, partitionNames) {
				t.Fatalf("job=%+v journal=%+v calls=%v", job, e.Journal, fb.calls)
			}
			if later {
				if fb.calls[0] != "oem dump fsc" || !slices.Contains(fb.calls, "reboot-bootloader") || !slices.Contains(fb.calls, "flash:partition") || e.Journal.Choice != "repair" {
					t.Fatal("Repair did not run its full canonical procedure", fb.calls, e.Journal)
				}
			} else if fb.calls[0] != "flash:tz" || slices.Contains(fb.calls, "oem dump fsc") || slices.Contains(fb.calls, "reboot-bootloader") || e.Journal.Choice != "stock" {
				t.Fatal("older-only repair restarted the chain", fb.calls, e.Journal)
			}
		})
	}
}

func TestVerifiedSystemHistoryAllowsSystemInstall(t *testing.T) {
	older, newer := chainHistory(t)
	e, plan, fb, _ := executorFixture(t, "system", profile.ModeFastboot, "LK1ST_MSM8916")
	for _, j := range []Journal{older, newer} {
		if err := e.Journals.Save(t.Context(), j); err != nil {
			t.Fatal(err)
		}
	}
	plan.Serial, plan.Start.Serial = older.Serial, older.Serial
	fb.serial = older.Serial
	e.ADB.(*fakeConnector).d.serial = older.Serial
	job := e.Run(t.Context(), plan)
	if job.Status != "succeeded" || e.Journal.RestoreSet != "" || len(fb.restored) != 0 || slices.Contains(fb.calls, "flash:partition") {
		t.Fatalf("system install blocked or repaired the chain: job=%+v calls=%v", job, fb.calls)
	}
}

func TestNewerJournalRequiresCompletedBootProofForSameBoard(t *testing.T) {
	for _, tc := range []struct {
		name, plan, proof, inFlight, profile, serial string
		finished, older, sameTime, superseded        bool
	}{
		{name: "completed chain reboot", plan: "repair", proof: "chain-reboot", superseded: true},
		{name: "verified Linux before later failure", proof: "verify-linux", inFlight: "relay/install", superseded: true},
		{name: "confirmed Linux system", proof: "confirm-linux", superseded: true},
		{name: "confirmed Linux repair", plan: "repair", proof: "confirm-linux", superseded: true},
		{name: "legacy system confirmation", proof: "confirm", superseded: true},
		{name: "stock confirmation", plan: "stock", proof: "confirm"},
		{name: "ambiguous legacy repair confirmation", plan: "repair", proof: "confirm"},
		{name: "Linux reboot alone", proof: "boot-linux"},
		{name: "verification only in flight", inFlight: "verify-linux"},
		{name: "finished alone", finished: true},
		{name: "older verification", proof: "verify-linux", older: true},
		{name: "same start time", proof: "verify-linux", sameTime: true},
		{name: "other serial", proof: "verify-linux", serial: "other-board"},
		{name: "other profile", proof: "verify-linux", profile: "other-profile"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			older, newer := chainHistory(t)
			newer.Plan, newer.Choice = "system", "base"
			if tc.plan != "" {
				newer.Plan = tc.plan
			}
			newer.Completed, newer.CompletedAt = nil, nil
			if tc.proof != "" {
				newer.Completed = []string{tc.proof}
			}
			newer.InFlight, newer.Finished = tc.inFlight, tc.finished
			if tc.older {
				newer.Started = older.Started.Add(-time.Minute)
			}
			if tc.sameTime {
				newer.Started = older.Started
			}
			if tc.profile != "" {
				newer.Profile = tc.profile
			}
			if tc.serial != "" {
				newer.Serial = tc.serial
			}
			journals := Journals{Dir: t.TempDir()}
			for _, j := range []Journal{older, newer} {
				if tc.sameTime && j.Plan == "system" {
					// The legacy and versioned formats can contain separate jobs with equal starts.
					data, err := json.Marshal(j)
					if err != nil {
						t.Fatal(err)
					}
					dir, _ := journals.path(j.Profile, j.Serial)
					put(t, dir+".json", data)
				} else if err := journals.Save(t.Context(), j); err != nil {
					t.Fatal(err)
				}
			}
			source, err := journals.RepairSource(t.Context(), older.Profile, older.Serial)
			if err != nil || source == nil || IncompleteChain(source) == tc.superseded {
				t.Fatalf("source=%+v err=%v", source, err)
			}
		})
	}
}

func TestBootProofDoesNotHideANewerIncompleteChain(t *testing.T) {
	older, proof := chainHistory(t)
	latest := older
	latest.Started = proof.Started.Add(time.Minute)
	latest.Updated = latest.Started
	latest.CapturedSet, latest.RestoreSet = "20261022T030034Z", "20261022T030034Z"
	// Even a later pre-chain failure cannot hide the newest incomplete chain.
	preChain := proof
	preChain.Started = latest.Started.Add(time.Minute)
	preChain.Completed, preChain.InFlight, preChain.Finished = []string{"backup"}, "reset-boot", false
	journals := Journals{Dir: t.TempDir()}
	for _, j := range []Journal{older, proof, latest, preChain} {
		if err := journals.Save(t.Context(), j); err != nil {
			t.Fatal(err)
		}
	}
	source, err := journals.RepairSource(t.Context(), older.Profile, older.Serial)
	if err != nil || source == nil || !IncompleteChain(source) || source.RepairSet() != latest.RepairSet() {
		t.Fatalf("source=%+v err=%v", source, err)
	}
}
