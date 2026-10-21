// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package stage

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/adb"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/profile"
)

const MaxLog = 1 << 20

type StepProgress struct {
	Step
	Status string `json:"status"`
}

// Job is an immutable snapshot sent to the shells after each change. Installed is the last read
// of dpkg; Observed says whether that read describes the device after this job finished.
type Job struct {
	Retryable bool              `json:"retryable"`
	Transfer  *TransferProgress `json:"transfer,omitempty"`
	Profile   string            `json:"profile,omitempty"`
	Serial    string            `json:"serial,omitempty"`
	Plan      string            `json:"plan,omitempty"`
	Status    string            `json:"status"`
	Started   time.Time         `json:"started"`
	Finished  time.Time         `json:"finished,omitempty"`
	Steps     []StepProgress    `json:"steps"`
	Failed    int               `json:"failed"`
	Problem   *Problem          `json:"problem,omitempty"`
	Left      string            `json:"left,omitempty"`
	Installed *Facts            `json:"installed,omitempty"`
	Observed  bool              `json:"observed"`
	Log       string            `json:"log"`
	LogCapped bool              `json:"logCapped"`
	Written   []string          `json:"written,omitempty"`
	Pending   []string          `json:"pending,omitempty"`
	Uncertain string            `json:"uncertain,omitempty"`
	Findings  map[string]string `json:"findings,omitempty"`
}

type TransferProgress struct {
	Phase           string `json:"phase"`
	Piece           int    `json:"piece"`
	Pieces          int    `json:"pieces"`
	Done            int64  `json:"done"`
	Total           int64  `json:"total"`
	EstimateSeconds int64  `json:"estimateSeconds,omitempty"`
}

// Executor's connection and guard come from the service's fresh one-device scans. It never owns
// a bus or starts another job; the service holds its shared device lock for the entire run.
type Executor struct {
	// Confirm binds an existing connection to the job identity without opening a second USB handle.
	Confirm        func(context.Context, adb.Device) error
	Checkpoint     func(context.Context, Step, string) error
	Connect        func(context.Context) (adb.Device, error)
	Guard          func(context.Context) error
	Profile        profile.Profile
	Packages       []*Package
	Emit           func(Job)
	Now            func() time.Time
	RebootTimeout  time.Duration
	VerifyTimeout  time.Duration
	PollInterval   time.Duration
	VerifyInterval time.Duration

	dev            adb.Device
	job            Job
	facts          Facts
	bootID         string
	wrote          bool
	allowDowngrade bool
}

func NewExecutor(p profile.Profile, packages []*Package) *Executor {
	return &Executor{Profile: p, Packages: packages, Now: time.Now, RebootTimeout: 120 * time.Second,
		VerifyTimeout: 90 * time.Second, PollInterval: 2 * time.Second, VerifyInterval: 5 * time.Second}
}

func (e *Executor) publish() {
	if e.Emit == nil {
		return
	}
	j := e.job
	j.Steps = append([]StepProgress(nil), j.Steps...)
	e.Emit(j)
}

func (e *Executor) log(s string) {
	e.job.Log += strings.ReplaceAll(s, "\r\n", "\n")
	if len(e.job.Log) > MaxLog {
		e.job.Log = e.job.Log[len(e.job.Log)-MaxLog:]
		e.job.LogCapped = true
	}
	e.publish()
}

func (e *Executor) run(ctx context.Context, command string) (adb.Result, error) {
	e.log("$ " + command + "\n")
	r, err := e.dev.Shell(ctx, command)
	e.log(string(r.Stdout) + string(r.Stderr) + fmt.Sprintf("\n[exit %d]\n", r.ExitCode))
	if err != nil {
		e.log(err.Error() + "\n")
	}
	return r, err
}

func (e *Executor) command(ctx context.Context, command, success string) error {
	r, err := e.run(ctx, command)
	if err != nil {
		return err
	}
	last := lastStage(string(r.Stdout))
	if matchesMarker(last, "OXP-STAGE relay rolled-back") {
		return &Problem{"rolled-back", ""}
	}
	if r.ExitCode != 0 {
		return fmt.Errorf("exit %d: %s", r.ExitCode, strings.TrimSpace(string(r.Stderr)))
	}
	if success != "" && !matchesMarker(last, success) {
		return &Problem{"success-line", success}
	}
	return nil
}

func lastStage(out string) string {
	last := ""
	for _, line := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		if strings.HasPrefix(line, "OXP-STAGE ") {
			last = line
		}
	}
	return last
}

func matchesMarker(line, want string) bool { return line == want || strings.HasPrefix(line, want+" ") }

func (e *Executor) inspect(ctx context.Context) (Facts, error) {
	if e.Confirm != nil {
		if err := e.Confirm(ctx, e.dev); err != nil {
			return Facts{}, err
		}
	}
	return Inspect(ctx, e.run, e.Profile, e.Packages)
}

func (e *Executor) confirm(f Facts, pkg *Package) error {
	base, _ := e.Profile.Layer("base")
	if base.DeviceTreeModel == "" || f.DeviceTreeModel != base.DeviceTreeModel {
		return &Problem{"unconfirmed", f.DeviceTreeModel}
	}
	if f.Layers["base"] == "" {
		return &Problem{"base-missing", ""}
	}
	for _, l := range e.Profile.Below(pkg.manifest.Stage) {
		if f.Layers[l.ID] == "" {
			return &Problem{"below-missing", l.ID}
		}
	}
	if problem := CheckRequirements(f, pkg); problem != nil {
		return problem
	}
	if pkg.manifest.Stage == "relay" && f.Layers["relay"] != "" && CompareVersions(pkg.manifest.Version, f.Layers["relay"]) < 0 && !e.allowDowngrade {
		return &Problem{"downgrade", pkg.manifest.Version}
	}
	return nil
}

// Run executes only a successful plan, in order. No later step runs after a failure.
func (e *Executor) Run(ctx context.Context, plan Plan) Job {
	e.allowDowngrade = plan.allowDowngrade
	e.job = Job{Status: "running", Started: e.Now(), Failed: -1, Steps: []StepProgress{}}
	for _, s := range plan.Steps {
		e.job.Steps = append(e.job.Steps, StepProgress{s, "pending"})
	}
	e.publish()
	defer func() {
		if e.dev != nil {
			e.dev.Close()
		}
	}()
	var err error
	if plan.Problem != nil {
		err = plan.Problem
	} else {
		e.dev, err = e.Connect(ctx)
	}
	if err != nil && len(e.job.Steps) > 0 {
		e.job.Steps[0].Status, e.job.Failed = "failed", 0
	}
	for i, s := range plan.Steps {
		if err != nil {
			break
		}
		e.job.Steps[i].Status = "running"
		e.publish()
		var pkg *Package
		for _, p := range e.Packages {
			if p.digest == s.PackageID {
				pkg = p
				break
			}
		}
		if e.Checkpoint != nil {
			err = e.Checkpoint(ctx, s, "running")
		}
		if err == nil {
			if pkg == nil {
				err = &Problem{"no-package", s.Layer}
			} else {
				err = e.step(ctx, s, pkg)
			}
		}
		if err == nil && e.Checkpoint != nil {
			err = e.Checkpoint(ctx, s, "ok")
		}
		if err != nil {
			e.job.Steps[i].Status, e.job.Failed = "failed", i
		} else {
			e.job.Steps[i].Status = "ok"
		}
		e.publish()
	}
	if err != nil {
		e.job.Status = "failed"
		var problem *Problem
		if errors.As(err, &problem) {
			e.job.Problem = problem
		} else {
			e.job.Problem = &Problem{"device", err.Error()}
		}
		e.job.Left = "unchanged"
		if e.wrote {
			e.job.Left = "partial"
		}
		if e.job.Problem.Code == "rolled-back" {
			e.job.Left = "previous-relay"
		}
		e.log("Stopped: " + err.Error() + "\n")
		// The command may have partially installed a layer. Read back what is actually installed;
		// a disconnected device keeps the last observation explicitly marked as stale.
		readCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		if guardErr := e.Guard(readCtx); guardErr == nil && e.dev != nil {
			if f, readErr := e.inspect(readCtx); readErr == nil {
				e.job.Installed, e.job.Observed = &f, true
			}
		}
		cancel()
	} else {
		e.job.Status, e.job.Left = "succeeded", "installed"
	}
	e.job.Finished = e.Now()
	if e.job.Status == "failed" && e.job.Observed && e.job.Failed >= 0 && e.job.Failed < len(e.job.Steps) {
		step := e.job.Steps[e.job.Failed]
		// Only verification can be resumed directly. A write, reboot or uncertain install
		// always goes back through a complete plan (or the base engine's Repair procedure).
		e.job.Retryable = !step.Critical && step.Resumable && (step.Action == "verify" || step.Action == "installed")
		for _, pkg := range e.Packages {
			if e.job.Installed.Layers[pkg.manifest.Stage] != pkg.manifest.Version {
				e.job.Retryable = false
			}
		}
	}
	e.publish()
	return e.job
}

func (e *Executor) step(ctx context.Context, s Step, pkg *Package) error {
	timeout := 2 * time.Minute
	if s.Action == "install" {
		timeout = 10 * time.Minute
	}
	stepCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if e.Confirm != nil && e.dev != nil {
		if err := e.Confirm(stepCtx, e.dev); err != nil {
			return err
		}
	}
	m := pkg.manifest
	remote := path.Join(m.DeviceDir, pkg.filename)
	dir := m.DeviceDir
	if m.Stage == "device" {
		dir = path.Join(dir, m.Name)
	}
	inDir := func(cmd string) string { return "cd " + quote(dir) + " && (\n" + cmd + "\n)" }
	// Even clock changes, staging and preflight can write. Recheck the one-device rule for each.
	switch s.Action {
	case "clock", "prepare", "push", "unpack", "preflight", "install", "reboot":
		if err := e.Guard(stepCtx); err != nil {
			return err
		}
	}
	switch s.Action {
	case "requirements":
		if err := e.Guard(stepCtx); err != nil {
			return err
		}
		f, err := e.inspect(stepCtx)
		if err != nil {
			return err
		}
		e.facts, e.job.Installed = f, &f
		return e.confirm(f, pkg)
	case "clock":
		return e.command(stepCtx, fmt.Sprintf("date -u -s @%d", e.Now().Unix()), "")
	case "prepare":
		return e.command(stepCtx, "umask 077; mkdir -p "+quote(m.DeviceDir), "")
	case "push":
		if err := pkg.Recheck(stepCtx); err != nil {
			return err
		}
		e.log("Push " + pkg.filename + " -> " + remote + "\n")
		return e.dev.Push(stepCtx, remote, pkg.data, 0o644)
	case "checksum":
		r, err := e.run(stepCtx, "sha256sum "+quote(remote))
		if err != nil {
			return err
		}
		fields := strings.Fields(string(r.Stdout))
		if r.ExitCode != 0 || len(fields) < 1 || fields[0] != pkg.digest {
			return &Problem{"device-checksum", remote}
		}
		return nil
	case "unpack":
		return e.command(stepCtx, "rm -rf "+quote(dir)+" && tar -xzmf "+quote(remote)+" -C "+quote(m.DeviceDir)+" && "+inDir("sha256sum -c SHA256SUMS"), "")
	case "preflight":
		return e.command(stepCtx, inDir(m.Preflight), m.Success.Preflight)
	case "install":
		e.wrote = true
		cmd := m.Install
		if m.Stage == "relay" && e.facts.Layers["relay"] == "" {
			// dpkg itself has no stage marker. The first-install command's successful exit is
			// adapted to the same marker contract; verification and dpkg readback still follow.
			cmd = "(" + m.FirstInstall + ") && printf '%s\\n' " + quote(m.Success.Install)
		}
		return e.command(stepCtx, inDir(cmd), m.Success.Install)
	case "reboot":
		r, err := e.run(stepCtx, "cat /proc/sys/kernel/random/boot_id")
		if err != nil {
			return err
		}
		e.bootID = strings.TrimSpace(string(r.Stdout))
		if r.ExitCode != 0 || e.bootID == "" {
			return errors.New("cannot read boot id before reboot")
		}
		// Queue the reboot in systemd so it survives the adb shell session ending.
		if err := e.command(stepCtx, "systemctl --no-block reboot", ""); err != nil {
			return err
		}
		e.dev.Close()
		e.dev = nil
		return nil
	case "wait":
		return e.waitForBoot(ctx)
	case "verify":
		if m.Stage == "device" {
			return e.verifyDevice(stepCtx, inDir(m.Verify), m.Success.Verify)
		}
		return e.command(stepCtx, inDir(m.Verify), m.Success.Verify)
	case "installed":
		f, err := e.inspect(stepCtx)
		if err != nil {
			return err
		}
		e.job.Installed, e.job.Observed = &f, true
		for _, used := range e.job.Steps {
			if used.Action != "install" {
				continue
			}
			for _, p := range e.Packages {
				if p.digest == used.PackageID && f.Layers[used.Layer] != p.manifest.Version {
					return &Problem{"installed-version", used.Layer}
				}
			}
		}
		return nil
	}
	return &Problem{"bad-step", s.Action}
}

func pause(ctx context.Context, interval time.Duration) error {
	t := time.NewTimer(interval)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (e *Executor) waitForBoot(ctx context.Context) error {
	waitCtx, cancel := context.WithTimeout(ctx, e.RebootTimeout)
	defer cancel()
	sameBoot := false
	for waitCtx.Err() == nil {
		attempt, stop := context.WithTimeout(waitCtx, 5*time.Second)
		d, err := e.Connect(attempt)
		// The old boot can answer briefly before shutdown; use the latest probe at timeout.
		sameBoot = false
		if err == nil {
			e.dev = d
			r, readErr := e.run(attempt, "cat /proc/sys/kernel/random/boot_id")
			bootID := strings.TrimSpace(string(r.Stdout))
			if readErr == nil && r.ExitCode == 0 && bootID != "" {
				if bootID != e.bootID {
					stop()
					e.log("adb returned after reboot\n")
					return nil
				}
				sameBoot = true
			}
			d.Close()
			e.dev = nil
		} else {
			e.log("Waiting for adb: " + err.Error() + "\n")
			var p *Problem
			if errors.As(err, &p) && p.Code != "no-device" && p.Code != "not-adb" {
				stop()
				return err
			}
		}
		stop()
		if err := pause(waitCtx, e.PollInterval); err != nil {
			break
		}
	}
	if sameBoot {
		return &Problem{"reboot-not-started", ""}
	}
	return &Problem{"reboot-timeout", ""}
}

func (e *Executor) verifyDevice(ctx context.Context, cmd, success string) error {
	verifyCtx, cancel := context.WithTimeout(ctx, e.VerifyTimeout)
	defer cancel()
	var last error
	for verifyCtx.Err() == nil {
		if err := e.Guard(verifyCtx); err != nil {
			return err
		}
		if e.Confirm != nil {
			if err := e.Confirm(verifyCtx, e.dev); err != nil {
				return err
			}
		}
		last = e.command(verifyCtx, cmd, success)
		if last == nil {
			return nil
		}
		if err := pause(verifyCtx, e.VerifyInterval); err != nil {
			break
		}
	}
	if last == nil {
		last = verifyCtx.Err()
	}
	return fmt.Errorf("device verification did not pass within %s: %w", e.VerifyTimeout, last)
}
