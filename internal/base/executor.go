// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package base

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/adb"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/device"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/fastboot"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/profile"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/stage"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/usb"
)

// stablePolls is how many consecutive scans must show the same fastboot connection before it is
// used; with the default 500 ms interval that is about a second.
const stablePolls = 3

type Executor struct {
	reconnect       *device.Candidate
	attempted       bool
	Profile         profile.Profile
	Files           *Files
	Store           *Store
	Journals        Journals
	Detector        device.Detector
	ADB             adb.Connector
	Open            OpenFastboot
	Emit            func(stage.Job)
	Progress        fastboot.Progress
	PollInterval    time.Duration
	FastbootTimeout time.Duration
	LinuxTimeout    time.Duration
	Continue        bool // The app completes the journal after the following stage executor succeeds.
	Choice          string
	Journal         *Journal
	previousChain   bool
	restoreSet      string
	fb              Fastboot
	candidate       *device.Candidate
	serial          string
	start           Identity
	job             stage.Job
}

func NewExecutor(p profile.Profile, files *Files, store *Store, journals Journals) *Executor {
	return &Executor{Profile: p, Files: files, Store: store, Journals: journals, Open: NativeFastboot, PollInterval: 500 * time.Millisecond, FastbootTimeout: 90 * time.Second, LinuxTimeout: 180 * time.Second}
}

func Steps(plan Plan) []stage.Step {
	out := []stage.Step{}
	for _, s := range plan.Steps {
		out = append(out, stage.Step{Layer: "base", Action: s.Action, ID: s.ID, Name: s.Name, Critical: s.Critical, Resumable: s.Resumable})
	}
	return out
}
func (e *Executor) publish() {
	if e.Emit == nil {
		return
	}
	j := e.job
	j.Steps = slices.Clone(j.Steps)
	j.Written = slices.Clone(j.Written)
	j.Pending = slices.Clone(j.Pending)
	j.Findings = maps.Clone(j.Findings)
	e.Emit(j)
}
func problem(err error) *stage.Problem {
	var p *stage.Problem
	if errors.As(err, &p) {
		return p
	}
	return &stage.Problem{Code: "base", Detail: err.Error()}
}
func (e *Executor) save(ctx context.Context) error {
	e.Journal.Updated = time.Now().UTC()
	return e.Journals.Save(ctx, *e.Journal)
}

// Run accepts only a canonical plan, persists intent before each command and completion after
// it. Critical windows get a non-cancellable context once entered and have no automatic retry.
func (e *Executor) Run(ctx context.Context, plan Plan) stage.Job {
	e.serial, e.start = plan.Serial, plan.Start
	e.previousChain, e.restoreSet = false, ""
	e.job = stage.Job{Profile: e.Profile.ID, Serial: plan.Serial, Plan: plan.Kind, Status: "running", Started: time.Now(), Failed: -1, Steps: []stage.StepProgress{}, Findings: map[string]string{}}
	for _, s := range Steps(plan) {
		e.job.Steps = append(e.job.Steps, stage.StepProgress{Step: s, Status: "pending"})
	}
	e.publish()
	defer func() {
		if e.fb != nil {
			e.fb.Close()
			e.fb = nil
		}
	}()
	var err error
	canonical := BuildPlan(e.Profile, plan.Kind, plan.Start)
	if plan.Problem != nil {
		err = plan.Problem
	} else if canonical.Problem != nil || !reflect.DeepEqual(canonical, plan) {
		err = &stage.Problem{Code: "bad-plan"}
	}
	if err == nil && (e.Detector == nil || e.Files == nil || e.Store == nil) {
		err = &stage.Problem{Code: "base-unavailable"}
	}
	if err == nil {
		err = e.Files.Prepare(ctx)
	}
	if err == nil {
		e.Store.Author = e.Files.AuthorHashes()
		if plan.Kind == "repair" {
			var previous *Journal
			previous, err = e.Journals.RepairSource(ctx, e.Profile.ID, plan.Serial)
			if previous != nil {
				e.previousChain, e.restoreSet = true, previous.RepairSet()
			}
		}
	}
	if err == nil {
		e.Journal = &Journal{Schema: JournalSchema, Profile: e.Profile.ID, Serial: plan.Serial, Plan: plan.Kind, Choice: e.Choice, Started: e.job.Started.UTC(), Completed: []string{}, CompletedAt: map[string]time.Time{}}
		e.Journal.RestoreSet = e.restoreSet
		err = e.save(ctx)
	}
	window := ""
	for i, s := range plan.Steps {
		if err != nil {
			break
		}
		if window != s.Window {
			if err = ctx.Err(); err != nil {
				e.job.Failed = i
				break
			}
			if s.Window == "chain" {
				if err = e.preflightChain(ctx, plan); err != nil {
					e.job.Failed = i
					break
				}
			}
			window = s.Window
		}
		stepCtx := ctx
		if s.Critical {
			stepCtx = context.WithoutCancel(ctx)
		} else if err = ctx.Err(); err != nil {
			e.job.Failed = i
			break
		}
		e.attempted = false
		e.job.Steps[i].Status = "running"
		e.Journal.InFlight = s.ID
		e.publish()
		if err = e.save(stepCtx); err == nil {
			err = e.step(stepCtx, s)
		}
		if err != nil {
			e.job.Steps[i].Status = "failed"
			e.job.Failed = i
			if e.attempted {
				e.job.Uncertain = s.ID
			}
		} else {
			e.job.Steps[i].Status = "ok"
			e.Journal.Completed = append(e.Journal.Completed, s.ID)
			e.Journal.CompletedAt[s.ID] = time.Now().UTC()
			e.Journal.InFlight = ""
			if s.Action == "flash" || s.Action == "restore" || s.Action == "erase" {
				e.job.Written = append(e.job.Written, s.ID)
			}
			err = e.save(stepCtx)
			if err != nil {
				e.job.Failed = i
			}
		}
		e.publish()
	}
	if err == nil && !e.Continue {
		e.Journal.Finished = true
		err = e.save(context.WithoutCancel(ctx))
	}
	e.job.Status = "succeeded"
	e.job.Left = "installed"
	if err != nil {
		e.job.Status = "failed"
		e.job.Problem = problem(err)
		e.job.Left = "unchanged"
		if len(e.job.Written) > 0 || e.job.Uncertain != "" {
			e.job.Left = "partial"
		}
		for _, s := range e.job.Steps {
			if s.Status != "ok" {
				e.job.Pending = append(e.job.Pending, s.ID)
			}
		}
		if e.job.Failed < 0 && len(e.job.Steps) > 0 {
			e.job.Failed = 0
			e.job.Steps[0].Status = "failed"
		}
	}
	e.job.Finished = time.Now()
	e.publish()
	return e.job
}

func sameConnection(a, b device.Candidate) bool {
	return a.USB.ID() == b.USB.ID() && a.USB.Location() == b.USB.Location() && a.USB.Address == b.USB.Address
}
func (e *Executor) fastboot(ctx context.Context, product string) error {
	c, err := OneCandidate(e.Detector.Scan(ctx), profile.ModeFastboot)
	if err != nil {
		return err
	}
	if e.fb == nil || e.candidate == nil || !sameConnection(*c, *e.candidate) {
		if e.fb != nil {
			e.fb.Close()
			e.fb = nil
		}
		e.fb, err = e.Open(ctx, c.USB)
		if err != nil {
			return err
		}
		e.candidate = c
	}
	// Fresh getvars on every write are cheap, and also guard a transport that has reconnected
	// without changing USB descriptors. Device serials are never inferred from adb's Linux serial.
	identity, err := ReadFastbootIdentity(ctx, e.fb)
	if err != nil {
		e.closeFastboot()
		return err
	}
	if identity.Serial != e.serial {
		return &stage.Problem{Code: "serial-mismatch", Detail: identity.Serial}
	}
	if identity.Product != product {
		return &stage.Problem{Code: "wrong-product", Detail: identity.Product}
	}
	return nil
}
func (e *Executor) closeFastboot() {
	if e.fb != nil {
		e.fb.Close()
	}
	e.fb = nil
	e.candidate = nil
}

func pause(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
func (e *Executor) waitFastboot(ctx context.Context, product string) error {
	wait, cancel := context.WithTimeout(ctx, e.FastbootTimeout)
	defer cancel()
	// The dongle re-enumerates for a moment after a reboot (fsqo.1): trust a connection only once
	// the same one has been seen on stablePolls consecutive scans.
	var seen *device.Candidate
	polls := 0
	for wait.Err() == nil {
		candidate, err := OneCandidate(e.Detector.Scan(wait), profile.ModeFastboot)
		if err == nil {
			if e.reconnect == nil || !sameConnection(*candidate, *e.reconnect) {
				if seen != nil && sameConnection(*candidate, *seen) {
					polls++
				} else {
					seen, polls = candidate, 1
				}
				if polls >= stablePolls {
					err = e.fastboot(wait, product)
					if err == nil {
						e.reconnect = nil
						return nil
					}
					seen, polls = nil, 0
					if !errors.Is(err, usb.ErrNotFound) {
						var p *stage.Problem
						if !errors.As(err, &p) || p.Code != "no-device" {
							return err
						}
					}
				}
			}
		} else {
			var p *stage.Problem
			if !errors.As(err, &p) || p.Code != "no-device" {
				return err
			}
			// Observing disappearance also supports transports that reuse a USB address.
			e.reconnect = nil
			seen, polls = nil, 0
		}
		if err = pause(wait, e.PollInterval); err != nil {
			break
		}
	}
	return &stage.Problem{Code: "fastboot-timeout"}
}
func (e *Executor) linux(ctx context.Context) (adb.Device, error) {
	c, err := OneCandidate(e.Detector.Scan(ctx), profile.ModeADB)
	if err != nil {
		return nil, err
	}
	b := e.Profile.Base
	if c.USB.ID() != b.Linux.VendorID+":"+b.Linux.ProductID || !c.Supported() || e.ADB == nil {
		return nil, &stage.Problem{Code: "wrong-mode"}
	}
	d, err := e.ADB.Connect(ctx, c.USB)
	if err != nil {
		return nil, err
	}
	serial, err := LinuxSerial(ctx, d)
	if err == nil && serial != e.serial {
		err = &stage.Problem{Code: "serial-mismatch", Detail: serial}
	}
	if err != nil {
		d.Close()
		return nil, err
	}
	return d, nil
}
func (e *Executor) waitLinux(ctx context.Context) error {
	wait, cancel := context.WithTimeout(ctx, e.LinuxTimeout)
	defer cancel()
	for wait.Err() == nil {
		d, err := e.linux(wait)
		if err == nil {
			err = e.verifyLinux(wait, d)
			d.Close()
			return err
		}
		var p *stage.Problem
		if errors.As(err, &p) && p.Code != "no-device" {
			return err
		}
		if err = pause(wait, e.PollInterval); err != nil {
			break
		}
	}
	return &stage.Problem{Code: "linux-timeout"}
}
func (e *Executor) verifyLinux(ctx context.Context, d adb.Device) error {
	f, err := stage.Inspect(ctx, d.Shell, e.Profile, nil)
	if err != nil {
		return err
	}
	b := e.Profile.Base.Linux
	if f.KernelRelease != b.KernelRelease || f.DeviceTreeModel != b.DeviceTreeModel || f.DebianVersion != b.DebianVersion {
		return &stage.Problem{Code: "linux-mismatch", Detail: f.KernelRelease + ", " + f.DeviceTreeModel + ", " + f.DebianVersion}
	}
	for _, cmd := range []string{"df -h /", "systemctl --failed --no-pager"} {
		r, err := d.Shell(ctx, cmd)
		if err != nil {
			return err
		}
		if r.ExitCode != 0 {
			return fmt.Errorf("%s: exit %d", cmd, r.ExitCode)
		}
		e.job.Findings[cmd] = strings.TrimSpace(string(r.Stdout))
	}
	e.job.Installed = &f
	e.job.Observed = true
	return nil
}

func (e *Executor) step(ctx context.Context, s Step) error {
	switch s.Action {
	case "confirm-stock", "adb-bootloader":
		mode := e.start.Mode
		c, err := OneCandidate(e.Detector.Scan(ctx), mode)
		if err != nil {
			return err
		}
		if c.USB.Location() != e.start.Location || e.ADB == nil {
			return &stage.Problem{Code: "serial-mismatch"}
		}
		d, err := e.ADB.Connect(ctx, c.USB)
		if err != nil {
			return err
		}
		defer d.Close()
		if mode == profile.ModeStock {
			identity, err := StockIdentity(ctx, d, e.Profile, adb.TransportSerial(d, c.USB.Serial))
			if err != nil {
				return err
			}
			if identity.Serial != e.serial {
				return &stage.Problem{Code: "serial-mismatch"}
			}
		} else {
			serial, err := LinuxSerial(ctx, d)
			if err != nil {
				return err
			}
			if serial != e.serial {
				return &stage.Problem{Code: "serial-mismatch"}
			}
		}
		if s.Action == "adb-bootloader" {
			return adb.RebootBootloader(ctx, d)
		}
		return nil
	case "confirm-linux":
		d, err := e.linux(ctx)
		if err != nil {
			return err
		}
		defer d.Close()
		return e.verifyLinux(ctx, d)
	case "wait-fastboot":
		return e.waitFastboot(ctx, s.Product)
	case "wait-linux":
		return e.waitLinux(ctx)
	case "backup":
		if e.previousChain {
			// Check before capturing: a missing same-second set must never be recreated with
			// fresh post-GPT bytes under the historical source's name.
			if err := e.validateRestore(ctx); err != nil {
				return err
			}
		}
		if err := e.fastboot(ctx, s.Product); err != nil {
			return err
		}
		captured, err := e.Store.Capture(ctx, e.serial, "oem-dump", func(ctx context.Context, name string, w io.Writer) error {
			if err := e.fastboot(ctx, s.Product); err != nil {
				return err
			}
			// OEM INFO may include calibration data. Do not publish response text or dump bytes.
			if _, err := e.fb.OEM(ctx, "dump "+name); err != nil {
				return &stage.Problem{Code: "backup-dump", Detail: name}
			}
			if _, err := e.fb.Upload(ctx, w, nil); err != nil {
				var p *stage.Problem
				if errors.As(err, &p) && p.Code == "backup-size" {
					// A writer refusal interrupts Upload; its fastboot client cannot be reused.
					e.closeFastboot()
					return &stage.Problem{Code: p.Code, Detail: name}
				}
				return &stage.Problem{Code: "backup-dump", Detail: name}
			}
			return nil
		})
		if err != nil {
			var p *stage.Problem
			invalid := errors.As(err, &p) && slices.Contains([]string{"backup-size", "backup-empty", "backup-author"}, p.Code)
			if !e.previousChain || !invalid {
				return err
			}
			e.job.Findings["backupFreshProblem"] = p.Code + ": " + p.Detail
		} else {
			e.Journal.CapturedSet = captured.Set
			e.job.Findings["backupSet"] = captured.Set
			e.job.Findings["backupComparison"] = "first / 首次备份"
			if captured.ComparisonError {
				e.job.Findings["backupComparison"] = "unavailable / 无法比对"
			}
			if captured.PreviousSet != "" {
				e.job.Findings["backupPreviousSet"] = captured.PreviousSet
				e.job.Findings["backupComparison"] = "matched / 相同"
				if !captured.MatchedPrevious {
					e.job.Findings["backupComparison"] = "different / 不同"
					e.job.Findings["backupDiffering"] = strings.Join(captured.Differing, ", ")
				}
			}
			if e.job.Plan != "system" && !e.previousChain {
				e.restoreSet = captured.Set
			}
		}
		if e.job.Plan != "system" {
			e.Journal.RestoreSet = e.restoreSet
			e.job.Findings["restoreSet"] = e.restoreSet
		}
		// Once a set is published, retain its reference even if cancellation or validation
		// stops this job before the chain. The step remains in flight until Run completes it.
		if err := e.save(context.WithoutCancel(ctx)); err != nil {
			return err
		}
		if e.job.Plan != "system" {
			return e.validateRestore(ctx)
		}
		return nil
	case "flash", "restore", "erase":
		return e.write(ctx, s)
	case "reboot", "reboot-bootloader":
		if err := e.fastboot(ctx, s.Product); err != nil {
			return err
		}
		var err error
		if s.Action == "reboot" {
			err = e.fb.Reboot(ctx)
		} else {
			err = e.fb.RebootBootloader(ctx)
		}
		if err == nil && e.candidate != nil {
			before := *e.candidate
			e.reconnect = &before
		}
		e.closeFastboot()
		return err
	}
	return &stage.Problem{Code: "bad-step", Detail: s.Action}
}

var pinnedPartitions = map[string]string{"partition": "gpt", "hyp": "hyp", "rpm": "rpm", "sbl1": "sbl1", "tz": "tz", "aboot": "aboot", "cdt": "cdt", "rootfs": "rootfs", "boot": "boot"}

// write enforces policy independently of plans. Nothing can flash/erase a protected partition
// except restore, with a freshly validated backup bound to the bootloader's current serial.
func (e *Executor) write(ctx context.Context, s Step) error {
	if protected(s.Partition) && s.Action != "restore" || s.Action == "restore" && !protected(s.Partition) {
		return &stage.Problem{Code: "protected-write", Detail: s.Partition}
	}
	if s.Action == "erase" && s.Partition != "boot" && s.Partition != "rootfs" {
		return &stage.Problem{Code: "unpinned-file", Detail: s.Partition}
	}
	if s.Action == "flash" && (pinnedPartitions[s.Partition] == "" || pinnedPartitions[s.Partition] != s.FileID) {
		return &stage.Problem{Code: "unpinned-file", Detail: s.FileID}
	}
	if s.Action != "flash" && s.Action != "restore" && s.Action != "erase" {
		return &stage.Problem{Code: "protected-write"}
	}
	if err := e.fastboot(ctx, s.Product); err != nil {
		return err
	}
	if s.Action == "erase" {
		e.attempted = true
		e.job.Observed = false
		return e.fb.Erase(ctx, s.Partition)
	}
	var img *fastboot.Image
	var err error
	if s.Action == "restore" {
		img, err = e.Store.OpenRestore(ctx, e.serial, e.restoreSet, s.Partition)
		if err != nil && ctx.Err() == nil {
			err = &stage.Problem{Code: "backup-restore-set", Detail: e.restoreSet}
		}
	} else {
		img, err = e.Files.Open(ctx, s.FileID)
	}
	if err != nil {
		return err
	}
	defer img.Close()
	// Checking a large file can take time. Re-scan and re-read identity again at the write boundary.
	if err = e.fastboot(ctx, s.Product); err != nil {
		return err
	}
	return e.fb.Flash(ctx, s.Partition, img, fastboot.FlashOptions{MaxPiece: e.Profile.Base.Fastboot.MaxPiece, BeforeWrite: func(ctx context.Context) error {
		c, err := OneCandidate(e.Detector.Scan(ctx), profile.ModeFastboot)
		if err != nil {
			return err
		}
		if e.candidate == nil || !sameConnection(*c, *e.candidate) {
			return &stage.Problem{Code: "reconnected"}
		}
		e.attempted = true
		e.job.Observed = false
		return nil
	}}, e.Progress)
}

// preflightChain checks the entire restore set and every bootloader input before the first GPT
// write. It is still cancellable; the critical window begins only once these checks pass.
func (e *Executor) preflightChain(ctx context.Context, plan Plan) error {
	if err := e.validateRestore(ctx); err != nil {
		return err
	}
	for _, s := range plan.Steps {
		if s.Window == "chain" && s.Action == "flash" {
			img, err := e.Files.Open(ctx, s.FileID)
			if err != nil {
				return err
			}
			img.Close()
		}
	}
	return e.fastboot(ctx, e.Profile.Base.Fastboot.Product)
}

func (e *Executor) validateRestore(ctx context.Context) error {
	if _, err := e.Store.Validate(ctx, e.serial, e.restoreSet); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return &stage.Problem{Code: "backup-restore-set", Detail: e.restoreSet}
	}
	return nil
}
