// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package app

import (
	"context"
	"errors"
	"net"
	"slices"
	"strconv"
	"time"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/adb"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/base"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/device"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/power"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/profile"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/stage"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/usb"
)

const EventInstall = "flasher:install"

var ErrDeviceBusy = errors.New("a device operation is already running")

type InstallTarget struct {
	Device  device.Candidate `json:"device"`
	Profile string           `json:"profile"`
}

type InstallState struct {
	FileCheck      *FileCheck           `json:"fileCheck,omitempty"`
	FileResults    []FileResult         `json:"fileResults"`
	NextSteps      NextSteps            `json:"nextSteps"`
	Artifacts      []stage.ArtifactNeed `json:"artifacts"`
	Catalog        CatalogState         `json:"catalog"`
	Identity       *base.Identity       `json:"identity,omitempty"`
	Resume         *base.Journal        `json:"resume,omitempty"`
	Target         *InstallTarget       `json:"target,omitempty"`
	Installed      *stage.Facts         `json:"installed,omitempty"`
	Packages       []stage.PackageInfo  `json:"packages"`
	Layers         []stage.LayerRow     `json:"layers"`
	Choices        []stage.Choice       `json:"choices"`
	Suggested      string               `json:"suggested"`
	InstalledStale bool                 `json:"installedStale"`
	Problem        *stage.Problem       `json:"problem,omitempty"`
	Job            *stage.Job           `json:"job,omitempty"`
}

func installProblem(err error) *stage.Problem {
	var p *stage.Problem
	if errors.As(err, &p) {
		return p
	}
	for _, entry := range []struct {
		err  error
		code string
	}{
		{ErrNoADB, "no-adb"}, {ErrNoDevice, "no-device"}, {ErrSeveralDevices, "several-devices"},
		{usb.ErrTransfersUnsupported, "fastboot-unsupported"}, {ErrNotADB, "not-adb"}, {ErrUnconfirmed, "unconfirmed"}, {ErrDeviceBusy, "busy"},
	} {
		if errors.Is(err, entry.err) {
			return &stage.Problem{Code: entry.code}
		}
	}
	return &stage.Problem{Code: "device", Detail: err.Error()}
}

// installDevice always scans now: adb serial numbers and the watch's cached snapshot cannot
// establish the one-device rule at a write boundary.
func (s *Service) installDevice(ctx context.Context) (InstallTarget, profile.Profile, error) {
	s.mu.Lock()
	connector := s.adb
	s.mu.Unlock()
	if connector == nil && s.Connection().Device == nil {
		return InstallTarget{}, profile.Profile{}, ErrNoADB
	}
	snap := s.selectedSnapshot(s.scanDevices(ctx, false))
	if snap.Error != "" {
		return InstallTarget{}, profile.Profile{}, &stage.Problem{Code: "usb", Detail: snap.Error}
	}
	var supported []device.Candidate
	for _, c := range snap.Candidates {
		if c.Supported() {
			supported = append(supported, c)
		}
	}
	if len(supported) == 0 {
		return InstallTarget{}, profile.Profile{}, ErrNoDevice
	}
	if len(supported) != 1 {
		return InstallTarget{}, profile.Profile{}, ErrSeveralDevices
	}
	c := supported[0]
	if c.Mode != device.ModeADB && c.Mode != device.ModeSSH {
		return InstallTarget{}, profile.Profile{}, ErrNotADB
	}
	if c.Mode == device.ModeSSH && s.Connection().Device == nil {
		return InstallTarget{}, profile.Profile{}, &stage.Problem{Code: "ssh-login"}
	}
	if len(c.Profiles) != 1 {
		return InstallTarget{}, profile.Profile{}, ErrUnconfirmed
	}
	for _, p := range s.profiles {
		if p.ID == c.Profiles[0] {
			return InstallTarget{c, p.ID}, p, nil
		}
	}
	return InstallTarget{}, profile.Profile{}, ErrUnconfirmed
}

func (s *Service) installSnapshotLocked() InstallState {
	state := InstallState{FileCheck: s.fileCheck, FileResults: slices.Clone(s.fileResults), NextSteps: s.nextSteps, Catalog: s.catalogState, Identity: s.baseIdentity, Resume: s.unfinished, Target: s.installTarget, Installed: s.installed, Packages: stage.PackageInfos(s.packages),
		Layers: []stage.LayerRow{}, Choices: []stage.Choice{}, Problem: s.installProblem, Job: s.installJob, InstalledStale: s.installStale}
	if state.Job != nil {
		job := *state.Job
		job.Log = s.catalogLog + s.installCatalogLog + job.Log
		state.Job = &job
	}
	for _, key := range sortedKeys(s.artifacts) {
		state.Artifacts = append(state.Artifacts, s.artifacts[key])
	}
	var p profile.Profile
	if state.Target != nil {
		for _, candidate := range s.profiles {
			if candidate.ID == state.Target.Profile {
				p = candidate
			}
		}
	}
	if state.Installed != nil {
		state.Choices = stage.BuildChoices(p, *state.Installed, s.preparedPackagesLocked(s.packages), false)
		for i := range state.Choices {
			if state.Problem != nil {
				state.Choices[i].Problem, state.Choices[i].Ready = state.Problem, false
			}
		}
		state.Layers = stage.LayerRows(p, *state.Installed, state.Choices)
		state.Suggested = stage.SuggestedChoice(state.Choices, *state.Installed)
		s.extendBaseChoicesLocked(&state, p)
	}
	return state
}

// InstallSnapshot returns cached state without contacting the device.
func (s *Service) InstallSnapshot() InstallState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.installSnapshotLocked()
}

func (s *Service) emitInstall() InstallState {
	state := s.InstallSnapshot()
	if s.events != nil {
		s.events.Emit(EventInstall, state)
	}
	return state
}

func (s *Service) inspectInstall(ctx context.Context, choice string) (profile.Profile, error) {
	if handled, p, err := s.inspectBaseTarget(ctx); handled {
		return p, err
	}
	target, p, err := s.installDevice(ctx)
	var facts stage.Facts
	packageProblems := map[string]*stage.Problem{}
	if err == nil {
		s.mu.Lock()
		packages := slices.Clone(s.packages)
		s.mu.Unlock()
		if choice != "" {
			packages = choicePackages(p, packages, choice)
		}
		var d adb.Device
		d, err = s.openDevice(ctx, target.Device)
		if err == nil {
			serial, serialErr := s.boardSerial(ctx, d)
			s.mu.Lock()
			s.baseIdentity = nil
			s.unfinished = nil
			if serialErr == nil {
				location := target.Device.USB.Location()
				if target.Device.Mode == profile.ModeSSH {
					location = net.JoinHostPort(target.Device.Host, strconv.Itoa(target.Device.Port))
				}
				s.baseIdentity = &base.Identity{Mode: target.Device.Mode, Serial: serial, Location: location}
			}
			s.mu.Unlock()
			// Discover installed layers from the profile alone; package-specific observations
			// belong to Prepare and cannot make choosing an unrelated layer fail.
			facts, err = stage.Inspect(ctx, d.Shell, p, nil)
			if err == nil {
				for _, pkg := range packages {
					m := pkg.Manifest()
					if m.Device != "" && m.Device != p.ID {
						continue
					}
					checked, readErr := stage.InspectPackageRequirements(ctx, d.Shell, facts, pkg)
					if readErr != nil {
						packageProblems[m.Stage] = installProblem(readErr)
					} else {
						facts = checked
					}
				}
			}
			if err == nil && facts.Layers["relay"] != "" && serialErr == nil {
				s.observeIdentity(ctx, d, serial)
			}
			d.Close()
			if err == nil && serialErr == nil && p.Base != nil {
				err = s.loadJournal(ctx, p, serial)
			}
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.installProblem = nil
	if err != nil {
		s.installProblem = installProblem(err)
		s.installStale = s.installed != nil
	} else {
		s.installTarget, s.installed = &target, &facts
		s.packageProblems = packageProblems
		s.installStale = false
	}
	return p, err
}

// Install refreshes read-only device facts when the shared device lock is available. During a job
// its cached progress is returned immediately, so polling never opens a second adb connection.
func (s *Service) Install(ctx context.Context) InstallState {
	if s.configMu.TryLock() {
		readCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		_, _ = s.inspectInstall(readCtx, "")
		cancel()
		s.configMu.Unlock()
	}
	return s.emitInstall()
}

// InstallAdd loads and checks a local file. Loading another version replaces that layer's package.
func (s *Service) InstallAdd(filename string) (InstallState, error) {
	return s.InstallAddForLayer("", filename)
}

func (s *Service) InstallAddForLayer(layer, filename string) (InstallState, error) {
	return s.selectOne(context.Background(), "", layer, "", filename)
}

func (s *Service) InstallRemove(id string) (InstallState, error) {
	if !s.configMu.TryLock() {
		return s.InstallSnapshot(), ErrDeviceBusy
	}
	defer s.configMu.Unlock()
	s.mu.Lock()
	for _, pkg := range s.packages {
		if pkg.Info().ID == id {
			layer := pkg.Manifest().Stage
			for key, f := range s.picks {
				if f.Layer == layer {
					delete(s.picks, key)
					delete(s.artifacts, key)
					delete(s.checkedFiles, f.SHA256)
				}
			}
		}
	}
	s.packages = slices.DeleteFunc(s.packages, func(p *stage.Package) bool { return p.Info().ID == id })
	s.mu.Unlock()
	err := s.saveSelections(context.Background())
	return s.emitInstall(), err
}

// InstallStart re-plans against fresh facts under the same lock held until the job finishes. The
// request may end after this returns; the service lifetime, not the HTTP connection, owns the job.
func (s *Service) InstallStart(ctx context.Context, req stage.Request) (InstallState, error) {
	s.ForwardStop()
	if req.BasePlan != "" && !isBaseChoice(req.Choice) {
		return s.InstallSnapshot(), &stage.Problem{Code: "bad-plan"}
	}
	if isBaseChoice(req.Choice) {
		return s.startBase(ctx, req)
	}
	return s.startLayers(ctx, req, nil)
}

// InstallRetry accepts only an engine-offered verification retry for the exact finished job.
// The package digest, model, serial and fresh requirements are rechecked below. No install or
// reboot command is part of this retry; those require a new plan or Repair.
func (s *Service) InstallRetry(ctx context.Context, started time.Time) (InstallState, error) {
	s.mu.Lock()
	job, req := s.installJob, s.lastInstall
	if job == nil || job.Status != "failed" || !job.Retryable || !job.Started.Equal(started) || isBaseChoice(req.Choice) {
		s.mu.Unlock()
		return s.InstallSnapshot(), &stage.Problem{Code: "retry-unavailable"}
	}
	copy := *job
	s.mu.Unlock()
	return s.startLayers(ctx, req, &copy)
}

func (s *Service) startLayers(ctx context.Context, req stage.Request, retry *stage.Job) (InstallState, error) {
	if !s.configMu.TryLock() {
		return s.InstallSnapshot(), ErrDeviceBusy
	}
	p, err := s.inspectInstall(ctx, req.Choice)
	if err != nil {
		s.configMu.Unlock()
		return s.emitInstall(), err
	}
	s.mu.Lock()
	packages, facts := choicePackages(p, s.packages, req.Choice), *s.installed
	var selected *stage.Choice
	for _, c := range stage.BuildChoices(p, facts, s.preparedPackagesLocked(packages), req.Downgrade) {
		if c.ID == req.Choice {
			selected = &c
			break
		}
	}
	var problem *stage.Problem
	if selected == nil {
		problem = &stage.Problem{Code: "bad-choice", Detail: req.Choice}
	} else if selected.Problem != nil {
		problem = selected.Problem
	} else if !selected.Ready {
		for _, need := range selected.Needs {
			if need.Problem != nil {
				problem = need.Problem
				break
			}
			if !need.Ready {
				problem = &stage.Problem{Code: "no-package", Detail: need.Layer}
				break
			}
		}
	}
	if problem != nil {
		s.mu.Unlock()
		s.configMu.Unlock()
		return s.emitInstall(), problem
	}
	plan := stage.BuildPlan(p, facts, packages, req)
	if plan.Problem != nil {
		s.mu.Unlock()
		s.configMu.Unlock()
		return s.emitInstall(), plan.Problem
	}
	serial := ""
	if s.baseIdentity != nil {
		serial = s.baseIdentity.Serial
	}
	if retry != nil {
		valid := s.installJob != nil && s.installJob.Started.Equal(retry.Started) && s.installJob.Status == "failed" && s.installJob.Retryable && serial != "" && serial == retry.Serial
		index := -1
		if valid && retry.Failed >= 0 && retry.Failed < len(retry.Steps) {
			failed := retry.Steps[retry.Failed].Step
			for i, step := range plan.Steps {
				if step.Layer == failed.Layer && step.Action == failed.Action && step.PackageID == failed.PackageID {
					index = i
					break
				}
			}
		}
		if index >= 0 {
			for _, step := range plan.Steps[index:] {
				valid = valid && !step.Critical && step.Resumable && (step.Action == "verify" || step.Action == "installed")
			}
			for _, pkg := range packages {
				valid = valid && facts.Layers[pkg.Manifest().Stage] == pkg.Manifest().Version
			}
		} else {
			valid = false
		}
		if !valid {
			s.mu.Unlock()
			s.configMu.Unlock()
			return s.emitInstall(), &stage.Problem{Code: "retry-unavailable"}
		}
		plan.Steps = slices.Clone(plan.Steps[index:])
	}
	// Acquire before publishing a running job or executing any device write.
	inhibitor := s.sleepInhibitor
	if inhibitor == nil {
		inhibitor = power.Noop{}
	}
	release, err := inhibitor.Acquire()
	if err != nil {
		s.mu.Unlock()
		s.configMu.Unlock()
		return s.emitInstall(), &stage.Problem{Code: "keep-awake", Detail: err.Error()}
	}
	life := s.lifeCtx
	if life == nil {
		life = context.Background()
	}
	jobCtx, cancel := context.WithCancel(life)
	done := make(chan struct{})
	s.installCancel, s.installDone = cancel, done
	initial := stage.Job{Profile: p.ID, Serial: serial, Status: "running", Started: time.Now(), Failed: -1, Steps: []stage.StepProgress{}}
	for _, step := range plan.Steps {
		initial.Steps = append(initial.Steps, stage.StepProgress{Step: step, Status: "pending"})
	}
	s.installJob = &initial
	s.installCatalogLog = "Install / 安装: " + s.signatureLogLocked()
	s.lastInstall = req
	s.installStale = true
	s.mu.Unlock()
	executor := stage.NewExecutor(p, packages)
	if s.newExecutor != nil {
		executor = s.newExecutor(p, packages)
	}
	if serial != "" {
		executor.Confirm = func(ctx context.Context, d adb.Device) error {
			actual, err := s.boardSerial(ctx, d)
			if err != nil {
				return err
			}
			if actual != serial {
				return &stage.Problem{Code: "serial-mismatch"}
			}
			return nil
		}
	}
	executor.Guard = func(ctx context.Context) error {
		t, _, err := s.installDevice(ctx)
		if err != nil {
			return installProblem(err)
		}
		if t.Profile != p.ID {
			return &stage.Problem{Code: "wrong-device", Detail: t.Profile}
		}
		return nil
	}
	executor.Connect = func(ctx context.Context) (adb.Device, error) {
		t, _, err := s.installDevice(ctx)
		if err != nil {
			return nil, installProblem(err)
		}
		if t.Profile != p.ID {
			return nil, &stage.Problem{Code: "wrong-device", Detail: t.Profile}
		}
		d, err := s.openDevice(ctx, t.Device)
		if err == nil {
			s.mu.Lock()
			s.installTarget = &t
			s.mu.Unlock()
		}
		return d, err
	}
	executor.Emit = func(job stage.Job) {
		job.Profile, job.Serial = p.ID, serial
		job.Retryable = job.Retryable && serial != ""
		// The executor logs a failure before attempting its final readback. Keep the warning
		// and close guard armed until that readback has finished, too.
		if job.Finished.IsZero() {
			job.Status = "running"
		}
		s.mu.Lock()
		s.installJob = &job
		if job.Installed != nil {
			s.installed = job.Installed
		}
		s.installStale = job.Status == "running" || !job.Observed
		s.mu.Unlock()
		s.emitInstall()
	}
	state := s.emitInstall()
	go func() {
		defer close(done)
		defer cancel()
		defer s.configMu.Unlock()
		defer release()
		job := executor.Run(jobCtx, plan)
		if job.Status == "succeeded" {
			_, _ = s.inspectInstall(jobCtx, "")
			s.emitInstall()
		}
	}()
	return state, nil
}

type preparedPackages struct {
	stage.PackageSet
	problems map[string]*stage.Problem
}

func (p preparedPackages) ForLayer(layer string) (*stage.Package, *stage.Problem) {
	pkg, problem := p.PackageSet.ForLayer(layer)
	if problem == nil && pkg != nil {
		problem = p.problems[layer]
	}
	return pkg, problem
}

// choicePackages also isolates final BuildPlan and device inspection from unrelated payloads.
func choicePackages(p profile.Profile, packages []*stage.Package, id string) []*stage.Package {
	var needs []string
	for _, def := range stage.ChoiceDefinitions(p) {
		if def.ID == id {
			needs = def.Needs
			break
		}
	}
	return slices.DeleteFunc(slices.Clone(packages), func(pkg *stage.Package) bool {
		return !slices.Contains(needs, pkg.Manifest().Stage)
	})
}

func (s *Service) InstallRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.installJob != nil && s.installJob.Status == "running"
}

func (s *Service) InstallCritical() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.installJob == nil || s.installJob.Status != "running" {
		return false
	}
	for _, step := range s.installJob.Steps {
		if step.Status == "running" && step.Critical {
			return true
		}
	}
	return false
}

// SetSleepInhibitor is shell wiring; tests and platforms without power APIs use the no-op.
func (s *Service) SetSleepInhibitor(inhibitor power.Inhibitor) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sleepInhibitor = inhibitor
}

// InstallWait is the CLI's wait on the same job the GUI follows through events.
func (s *Service) InstallWait(ctx context.Context) (InstallState, error) {
	s.mu.Lock()
	done := s.installDone
	s.mu.Unlock()
	if done != nil {
		select {
		case <-done:
		case <-ctx.Done():
			return s.InstallSnapshot(), ctx.Err()
		}
	}
	return s.InstallSnapshot(), nil
}

func (s *Service) InstallLog() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.installJob == nil {
		return s.catalogLog
	}
	return s.catalogLog + s.installCatalogLog + s.installJob.Log
}

func sortedKeys[V any](m map[string]V) []string { keys := mapKeys(m); slices.Sort(keys); return keys }

func (s *Service) preparedPackagesLocked(packages []*stage.Package) preparedPackages {
	problems := map[string]*stage.Problem{}
	for layer, problem := range s.packageProblems {
		problems[layer] = problem
	}
	for _, pkg := range packages {
		if problem := s.catalogPackageProblemLocked(pkg); problem != nil {
			problems[pkg.Manifest().Stage] = problem
		}
	}
	return preparedPackages{stage.PackageSet(packages), problems}
}
