// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-22

package base

import (
	"archive/zip"
	"bytes"
	"io"
	"slices"
	"testing"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/profile"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/testgpt"
)

func replaceFixtureFile(t *testing.T, f fixture, id string, data []byte) {
	t.Helper()
	pin := f.p.Base.Files[id]
	pin.Size, pin.SHA256 = int64(len(data)), hash(data)
	f.p.Base.Files[id] = pin
	path := f.archives[pin.Archive]
	z, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, entry := range z.File {
		r, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(r)
		r.Close()
		if err != nil {
			t.Fatal(err)
		}
		if entry.Name == pin.Member {
			body = data
		}
		dest, err := w.Create(entry.Name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := dest.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	z.Close()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	put(t, path, buf.Bytes())
	a := f.p.Base.Archives[pin.Archive]
	a.Size, a.SHA256 = int64(buf.Len()), hash(buf.Bytes())
	f.p.Base.Archives[pin.Archive] = a
}

func TestStockPlanUsesNewGPTDespiteStaleGetvars(t *testing.T) {
	e, plan, fb, _ := executorFixture(t, "stock", profile.ModeStock, "MSM8916")
	fb.stockSizes = map[string]int64{"tz": 0x80000, "sbl1": 0x80000, "aboot": 0x100000, "modemst1": 0x180000}
	job := e.Run(t.Context(), plan)
	if job.Status != "succeeded" {
		t.Fatalf("job=%+v calls=%v", job, fb.calls)
	}
	if slices.Contains(fb.sizeQueries, "tz") || !slices.Equal(fb.sizeOptions["tz"], []int64{1 << 20}) {
		t.Fatalf("used stale size: queries=%v options=%v", fb.sizeQueries, fb.sizeOptions)
	}
	if !slices.Equal(fb.sizeOptions["aboot"], []int64{0, 1 << 20}) || !slices.Equal(fb.sizeOptions["modemst1"], []int64{2 << 20}) {
		t.Fatal("sizes not scoped to the rewritten table", fb.sizeOptions)
	}
	if !slices.Equal(fb.sizeOptions["boot"], []int64{0}) || !slices.Equal(fb.sizeOptions["rootfs"], []int64{0}) {
		t.Fatal("GPT override survived the chain reboot", fb.sizeOptions)
	}
}

func TestNewGPTSizeProblemsStopBeforeChain(t *testing.T) {
	for _, failure := range []string{"image", "restore", "malformed GPT", "missing partition"} {
		t.Run(failure, func(t *testing.T) {
			e, plan, fb, _ := executorFixture(t, "stock", profile.ModeStock, "MSM8916")
			switch failure {
			case "image":
				replaceFixtureFile(t, fb.f, "tz", bytes.Repeat([]byte{0x74}, (1<<20)+1))
			case "restore":
				data := testgpt.Image(map[string]int64{"hyp": 512 << 10, "rpm": 512 << 10, "sbl1": 512 << 10, "tz": 1 << 20, "fsc": 1024, "fsg": 512, "modemst1": 2 << 20, "modemst2": 2 << 20, "aboot": 1 << 20, "cdt": 2048})
				replaceFixtureFile(t, fb.f, "gpt", data)
				fb.f.p.Base.Protected["fsg"] = []int64{64, 1024}
				fb.f.dumps["fsg"] = bytes.Repeat([]byte{99}, 1024)
			case "malformed GPT":
				data := testgpt.Base()
				data[568] ^= 1
				replaceFixtureFile(t, fb.f, "gpt", data)
			case "missing partition":
				replaceFixtureFile(t, fb.f, "gpt", testgpt.Image(map[string]int64{"aboot": 1 << 20}))
			}
			job := e.Run(t.Context(), plan)
			if job.Status != "failed" || job.Steps[job.Failed].ID != "chain-partition" || slices.Contains(fb.calls, "flash:partition") || slices.Contains(fb.calls, "flash:hyp") || IncompleteChain(e.Journal) {
				t.Fatalf("entered critical window: job=%+v calls=%v", job, fb.calls)
			}
		})
	}
}

func TestInterruptedChainContinuesAtTZWithOriginalBackup(t *testing.T) {
	e, plan, fb, _ := executorFixture(t, "stock", profile.ModeStock, "MSM8916")
	fb.fail = "flash:tz"
	if job := e.Run(t.Context(), plan); job.Uncertain != "chain-tz" {
		t.Fatal(job.Problem)
	}
	previous := *e.Journal
	safe, err := e.Store.Validate(t.Context(), plan.Serial, previous.RepairSet())
	if err != nil {
		t.Fatal(err)
	}
	start := Identity{Mode: profile.ModeFastboot, Product: fb.product, Serial: plan.Serial}
	remaining := BuildRepairPlan(e.Profile, start, &previous)
	if remaining.Problem != nil || remaining.Steps[0].ID != "chain-tz" {
		t.Fatalf("plan=%+v", remaining)
	}
	cdt := slices.IndexFunc(remaining.Steps, func(s Step) bool { return s.ID == "chain-cdt" })
	for _, s := range remaining.Steps[:cdt+1] {
		if s.Action == "backup" || s.Action == "reboot-bootloader" || s.Action == "erase" || s.Action == "reboot" {
			t.Fatal("unsafe prefix", s)
		}
	}
	fb.calls, fb.fail, fb.expectRestores = nil, "", safe.Partitions
	for name := range fb.f.dumps {
		fb.f.dumps[name] = make([]byte, 64)
	}
	job := e.Run(t.Context(), remaining)
	if job.Status != "succeeded" || fb.calls[0] != "flash:tz" || !slices.Equal(fb.restored, partitionNames) || e.Journal.RestoreSet != safe.Set || e.Journal.Choice != "stock" {
		t.Fatalf("job=%+v journal=%+v calls=%v", job, e.Journal, fb.calls)
	}
	if slices.Contains(fb.calls, "oem dump fsc") || slices.Contains(fb.calls, "reboot-bootloader") || slices.Contains(fb.calls, "flash:partition") {
		t.Fatal("restarted preparation", fb.calls)
	}
}

func TestIncompleteChainRejectsMissingOrChangedSessionAndOtherPlans(t *testing.T) {
	for _, change := range []string{"gone", "rebooted", "serial", "product", "full repair", "system"} {
		t.Run(change, func(t *testing.T) {
			e, plan, fb, det := executorFixture(t, "stock", profile.ModeStock, "MSM8916")
			fb.fail = "flash:tz"
			e.Run(t.Context(), plan)
			previous := *e.Journal
			start := Identity{Mode: profile.ModeFastboot, Product: fb.product, Serial: plan.Serial}
			remaining := BuildRepairPlan(e.Profile, start, &previous)
			switch change {
			case "gone":
				det.candidates = nil
			case "rebooted":
				det.candidates[0].USB.Address++
			case "serial":
				fb.serial = "other-board"
			case "product":
				fb.product = "MSM8916"
			case "full repair":
				remaining = BuildPlan(e.Profile, "repair", start)
			case "system":
				remaining = BuildPlan(e.Profile, "system", start)
			}
			fb.calls, fb.fail = nil, ""
			job := e.Run(t.Context(), remaining)
			if job.Status != "failed" || job.Left != "partial" || len(fb.calls) != 0 {
				t.Fatalf("unsafe continuation: job=%+v calls=%v", job, fb.calls)
			}
		})
	}
}
