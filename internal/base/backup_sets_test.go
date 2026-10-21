// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package base

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/profile"
)

func priorJob(t *testing.T, f fixture, captured, inFlight string, completed []string) Journal {
	t.Helper()
	started := time.Now().Add(-time.Hour).UTC()
	j := Journal{Schema: JournalSchema, Profile: f.p.ID, Serial: "board1", Plan: "stock", Choice: "stock", Started: started, Updated: started, CapturedSet: captured, InFlight: inFlight, Completed: completed}
	if err := f.journals.Save(t.Context(), j); err != nil {
		t.Fatal(err)
	}
	return j
}

func TestCaptureVersionedSets(t *testing.T) {
	for _, tc := range []struct {
		name    string
		changed []string
		count   int
	}{
		{"identical", nil, 1},
		{"changed EFS", []string{"modemst1", "modemst2"}, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := makeFixture(t)
			now := time.Date(2026, 10, 21, 12, 34, 56, 0, time.FixedZone("local", -7*60*60))
			f.store.Now = func() time.Time { return now }
			first := f.capture(t, "board1")
			if first.Set != "20261021T193456Z" {
				t.Fatal("set must use UTC", first.Set)
			}
			dir, _ := f.store.setPath("board1", first.Set)
			manifest, err := os.ReadFile(filepath.Join(dir, "backup.json"))
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range tc.changed {
				f.dumps[name][0]++
			}
			captured := f.capture(t, "board1")
			sets, err := f.store.Sets(t.Context(), "board1")
			if err != nil || len(sets) != tc.count || sets[0].Set != captured.Set {
				t.Fatalf("sets=%+v err=%v", sets, err)
			}
			if (captured.Set == first.Set) != (tc.count == 1) {
				t.Fatal("capture did not deduplicate or version", captured.Set)
			}
			if tc.count == 2 && captured.Set != first.Set+"-1" {
				t.Fatal("same-second collision", captured.Set)
			}
			kept, err := f.store.Validate(t.Context(), "board1", first.Set)
			if err != nil || !reflect.DeepEqual(kept, first) {
				t.Fatalf("changed the older set: %+v %v", kept, err)
			}
			after, err := os.ReadFile(filepath.Join(dir, "backup.json"))
			if err != nil || !bytes.Equal(manifest, after) {
				t.Fatal("rewrote the older manifest", err)
			}
		})
	}
}

func TestRestoreSourceByPlan(t *testing.T) {
	for _, tc := range []struct {
		name, kind, inFlight, completed, invalid string
		old                                      bool
	}{
		{name: "system changed EFS", kind: "system", inFlight: "chain-rpm"},
		{name: "stock uses fresh capture", kind: "stock", inFlight: "chain-rpm"},
		{name: "repair without earlier job", kind: "repair"},
		{name: "repair before earlier chain", kind: "repair", inFlight: "bootstrap-aboot"},
		{name: "repair in-flight chain", kind: "repair", inFlight: "chain-partition", old: true},
		{name: "repair completed chain step", kind: "repair", completed: "chain-tz", old: true},
		{name: "repair in-flight restore", kind: "repair", inFlight: "restore-fsc", old: true},
		{name: "repair completed restore", kind: "repair", completed: "restore-modemst2", old: true},
		{name: "repair invalid zero", kind: "repair", inFlight: "chain-rpm", invalid: "zero", old: true},
		{name: "repair invalid ff", kind: "repair", inFlight: "chain-rpm", invalid: "ff", old: true},
		{name: "repair invalid short", kind: "repair", inFlight: "chain-rpm", invalid: "short", old: true},
		{name: "repair invalid long", kind: "repair", inFlight: "chain-rpm", invalid: "long", old: true},
		{name: "repair invalid author", kind: "repair", inFlight: "chain-rpm", invalid: "author", old: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			product := "LK1ST_MSM8916"
			if tc.kind == "stock" {
				product = "MSM8916"
			}
			e, plan, fb, _ := executorFixture(t, tc.kind, profile.ModeFastboot, product)
			first := fb.f.capture(t, "board1")
			if tc.inFlight != "" || tc.completed != "" {
				var completed []string
				if tc.completed != "" {
					completed = []string{tc.completed}
				}
				priorJob(t, fb.f, first.Set, tc.inFlight, completed)
				// A newer failed attempt that never entered the chain cannot hide its source.
				newer := Journal{Schema: JournalSchema, Profile: e.Profile.ID, Serial: plan.Serial, Plan: "repair", Started: time.Now().Add(-30 * time.Minute), Updated: time.Now(), InFlight: "backup"}
				if err := e.Journals.Save(t.Context(), newer); err != nil {
					t.Fatal(err)
				}
			}
			fb.f.dumps["modemst1"][0]++
			fb.f.dumps["modemst2"][0]++
			switch tc.invalid {
			case "zero":
				fb.f.dumps["modemst1"] = make([]byte, 64)
			case "ff":
				fb.f.dumps["modemst1"] = bytes.Repeat([]byte{255}, 64)
			case "short":
				fb.f.dumps["modemst1"] = []byte{1}
			case "long":
				fb.f.dumps["modemst1"] = bytes.Repeat([]byte{1}, 65)
			case "author":
				fb.f.dumps["modemst1"] = bytes.Repeat([]byte{23}, 64)
			}
			fb.expectRestores = first.Partitions
			if !tc.old {
				fb.expectRestores = map[string]Partition{}
				for name, data := range fb.f.dumps {
					fb.expectRestores[name] = Partition{Size: int64(len(data)), SHA256: hash(data)}
				}
			}
			job := e.Run(t.Context(), plan)
			if job.Status != "succeeded" {
				t.Fatalf("problem=%v calls=%v", job.Problem, fb.calls)
			}
			if tc.invalid != "" {
				if e.Journal.CapturedSet != "" || job.Findings["backupFreshProblem"] == "" {
					t.Fatal("invalid capture should only be a finding", e.Journal, job.Findings)
				}
			} else if e.Journal.CapturedSet == first.Set || job.Findings["backupDiffering"] != "modemst1, modemst2" {
				t.Fatal("fresh capture was not recorded", e.Journal, job.Findings)
			}
			want := e.Journal.CapturedSet
			if tc.old {
				want = first.Set
			}
			if tc.kind == "system" {
				if e.Journal.RestoreSet != "" || len(fb.restored) != 0 || slices.Contains(fb.calls, "flash:partition") {
					t.Fatal("system restored or rebuilt the chain", fb.calls)
				}
			} else if e.Journal.RestoreSet != want || !slices.Equal(fb.restored, partitionNames) {
				t.Fatalf("restore set=%s want=%s restored=%v", e.Journal.RestoreSet, want, fb.restored)
			}
		})
	}
}

func TestInvalidFreshCaptureStillStopsWithoutEarlierChain(t *testing.T) {
	for _, kind := range []string{"stock", "system", "repair"} {
		t.Run(kind, func(t *testing.T) {
			product := "LK1ST_MSM8916"
			if kind == "stock" {
				product = "MSM8916"
			}
			e, plan, fb, _ := executorFixture(t, kind, profile.ModeFastboot, product)
			fb.f.capture(t, "board1")
			fb.f.dumps["modemst2"] = make([]byte, 64)
			job := e.Run(t.Context(), plan)
			if job.Problem == nil || job.Problem.Code != "backup-empty" || slices.Contains(fb.calls, "flash:partition") || len(fb.restored) != 0 {
				t.Fatalf("problem=%v calls=%v", job.Problem, fb.calls)
			}
		})
	}
}

func TestNeededSetStopsBeforeChain(t *testing.T) {
	for _, damage := range []string{"missing set", "missing file", "checksum", "metadata", "after capture"} {
		t.Run(damage, func(t *testing.T) {
			e, plan, fb, _ := executorFixture(t, "repair", profile.ModeFastboot, "LK1ST_MSM8916")
			first := fb.f.capture(t, "board1")
			priorJob(t, fb.f, first.Set, "chain-rpm", nil)
			dir, _ := e.Store.setPath("board1", first.Set)
			corrupt := func() { put(t, filepath.Join(dir, "modemst1.bin"), bytes.Repeat([]byte{7}, 64)) }
			switch damage {
			case "missing set":
				if err := os.RemoveAll(dir); err != nil {
					t.Fatal(err)
				}
			case "missing file":
				if err := os.Remove(filepath.Join(dir, "fsg.bin")); err != nil {
					t.Fatal(err)
				}
			case "checksum":
				corrupt()
			case "metadata":
				put(t, filepath.Join(dir, "backup.json"), []byte("{}"))
			case "after capture":
				fb.onWrite = func(cmd string) {
					if cmd == "erase:boot" {
						corrupt()
					}
				}
			}
			fb.f.dumps["modemst2"][0]++
			job := e.Run(t.Context(), plan)
			if job.Problem == nil || job.Problem.Code != "backup-restore-set" || job.Problem.Detail != first.Set || slices.Contains(fb.calls, "flash:partition") || len(fb.restored) != 0 {
				t.Fatalf("problem=%v calls=%v", job.Problem, fb.calls)
			}
			latest, err := e.Journals.Load(t.Context(), e.Profile.ID, plan.Serial)
			if err != nil || latest.RestoreSet != first.Set || (latest.CapturedSet != "") != (damage == "after capture") {
				t.Fatal("lost the selected source or a completed capture", latest, err)
			}
		})
	}
}

func TestRepairRetriesKeepOriginalSetAndJournalHistory(t *testing.T) {
	e, plan, fb, _ := executorFixture(t, "stock", profile.ModeFastboot, "MSM8916")
	fb.fail = "flash:rpm"
	if job := e.Run(t.Context(), plan); job.Uncertain != "chain-rpm" {
		t.Fatal("first job did not stop in the chain", job.Problem)
	}
	first := *e.Journal
	safe, err := e.Store.Validate(t.Context(), "board1", first.CapturedSet)
	if err != nil {
		t.Fatal(err)
	}
	fb.expectRestores = safe.Partitions
	fb.f.dumps["modemst1"][0]++
	plan = BuildPlan(e.Profile, "repair", Identity{Mode: profile.ModeFastboot, Product: "LK1ST_MSM8916", Serial: "board1"})
	if job := e.Run(t.Context(), plan); job.Uncertain != "chain-rpm" {
		t.Fatal("second job did not stop in the chain", job.Problem)
	}
	second := *e.Journal
	if second.CapturedSet == first.CapturedSet || second.RestoreSet != first.CapturedSet {
		t.Fatal("repair did not retain both sources", second)
	}
	fb.fail = ""
	fb.f.dumps["modemst1"] = make([]byte, 64)
	if job := e.Run(t.Context(), plan); job.Status != "succeeded" {
		t.Fatal("repeated repair lost the original safe set", job.Problem)
	}
	history, err := e.Journals.History(t.Context(), e.Profile.ID, "board1")
	if err != nil || len(history) != 3 || !reflect.DeepEqual(history[2], first) || !reflect.DeepEqual(history[1], second) {
		t.Fatalf("history=%+v err=%v", history, err)
	}
	if history[0].RestoreSet != first.CapturedSet || history[0].CapturedSet != "" || !history[0].Finished {
		t.Fatal("repeated repair selected its invalid capture", history[0])
	}
	entries, err := os.ReadDir(filepath.Join(e.Journals.Dir, "jobs", e.Profile.ID+"-board1"))
	if err != nil || len(entries) != 3 {
		t.Fatal("jobs must have separate files", entries, err)
	}
}

func TestLegacyBackupAndJournalRemainInPlace(t *testing.T) {
	e, plan, fb, _ := executorFixture(t, "repair", profile.ModeFastboot, "LK1ST_MSM8916")
	f := fb.f
	dir, _ := f.store.path("board1")
	b := Backup{Schema: BackupSchema, Profile: f.p.ID, Serial: "board1", Created: time.Now().Add(-2 * time.Hour).UTC(), FlasherVersion: "old", Partitions: map[string]Partition{}}
	for name, data := range f.dumps {
		put(t, filepath.Join(dir, name+".bin"), data)
		b.Partitions[name] = Partition{Size: int64(len(data)), SHA256: hash(data), Source: "oem-dump"}
	}
	data, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	var old map[string]json.RawMessage
	if err := json.Unmarshal(data, &old); err != nil {
		t.Fatal(err)
	}
	delete(old, "set")
	data, err = json.Marshal(old)
	if err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(dir, "backup.json"), data)
	if got := f.capture(t, "board1"); got.Set != "legacy" {
		t.Fatal("identical capture should reuse legacy", got.Set)
	}
	j := Journal{Schema: JournalSchema, Profile: f.p.ID, Serial: "board1", Plan: "stock", Started: b.Created, Updated: b.Created, InFlight: "chain-rpm"}
	journalData, err := json.Marshal(j)
	if err != nil {
		t.Fatal(err)
	}
	journalPath := filepath.Join(f.journals.Dir, "jobs", f.p.ID+"-board1.json")
	put(t, journalPath, journalData)
	fb.expectRestores = b.Partitions
	f.dumps["modemst2"][0]++
	job := e.Run(t.Context(), plan)
	if job.Status != "succeeded" || e.Journal.RestoreSet != "legacy" || e.Journal.CapturedSet == "legacy" {
		t.Fatal("repair did not read legacy backup and journal", job.Problem, e.Journal)
	}
	sets, err := f.store.Sets(t.Context(), "board1")
	if err != nil || len(sets) != 2 || sets[1].Set != "legacy" {
		t.Fatal("legacy listing", sets, err)
	}
	after, err := os.ReadFile(filepath.Join(dir, "backup.json"))
	if err != nil || !bytes.Equal(data, after) {
		t.Fatal("legacy manifest changed", err)
	}
	after, err = os.ReadFile(journalPath)
	if err != nil || !bytes.Equal(journalData, after) {
		t.Fatal("legacy journal changed", err)
	}
	if _, err := f.store.Validate(t.Context(), "board1", "legacy"); err != nil {
		t.Fatal("legacy files were moved or changed", err)
	}
}

func TestImportCreatesSetsAndListingOrder(t *testing.T) {
	f := makeFixture(t)
	now := time.Date(2026, 10, 21, 1, 2, 3, 0, time.UTC)
	f.store.Now = func() time.Time { return now }
	first := f.capture(t, "board1")
	dir, _ := f.store.setPath("board1", first.Set)
	for i := 1; i <= 12; i++ {
		imported, err := f.store.Import(t.Context(), dir, "board1")
		if err != nil || imported.Set == first.Set || !imported.Created.Equal(now) {
			t.Fatal("import did not add a dated set", imported, err)
		}
		for _, part := range imported.Partitions {
			if part.Source != "imported" {
				t.Fatal("wrong import source", part.Source)
			}
		}
	}
	// A later insertion with an earlier timestamp must still sort by capture time.
	now = now.Add(-time.Hour)
	f.dumps["modemst1"][0]++
	last := f.capture(t, "board1")
	f.capture(t, "board2")
	sets, err := f.store.Sets(t.Context(), "board1")
	if err != nil || len(sets) != 14 || sets[0].Set != first.Set+"-12" || sets[1].Set != first.Set+"-11" || sets[12].Set != first.Set || sets[13].Set != last.Set {
		t.Fatalf("wrong newest-first order: %+v err=%v", sets, err)
	}
	all, err := f.store.List(t.Context())
	if err != nil || len(all) != 15 || all[0].Serial != "board1" || all[14].Serial != "board2" {
		t.Fatal("list must group sets per serial", all, err)
	}
	for _, bad := range []string{"", "../legacy", "../../board2", "/tmp/set"} {
		if _, err := f.store.Validate(t.Context(), "board1", bad); err == nil {
			t.Fatal("accepted an unnamed or unsafe set", bad)
		}
	}
	if _, err := f.store.OpenRestore(t.Context(), "board2", first.Set, "fsg"); err == nil {
		t.Fatal("opened another serial's set")
	}
	if _, err := f.store.OpenRestore(t.Context(), "board1", first.Set, "boot"); err == nil {
		t.Fatal("opened an unprotected partition")
	}
	entries, err := os.ReadDir(filepath.Dir(dir))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			t.Fatal("left temporary capture files", entry.Name())
		}
	}
}
