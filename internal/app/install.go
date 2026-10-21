// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package app

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/adb"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/device"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/profile"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/stage"
)

const EventInstall = "flasher:install"

var ErrDeviceBusy = errors.New("a device operation is already running")

type InstallTarget struct {
	Device  device.Candidate `json:"device"`
	Profile string           `json:"profile"`
}

type InstallState struct {
	Target    *InstallTarget      `json:"target,omitempty"`
	Installed *stage.Facts        `json:"installed,omitempty"`
	Packages  []stage.PackageInfo `json:"packages"`
	Choices   []stage.Plan        `json:"choices"`
	Problem   *stage.Problem      `json:"problem,omitempty"`
	Job       *stage.Job          `json:"job,omitempty"`
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
		{ErrNotADB, "not-adb"}, {ErrUnconfirmed, "unconfirmed"}, {ErrDeviceBusy, "busy"},
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
	if connector == nil {
		return InstallTarget{}, profile.Profile{}, ErrNoADB
	}
	snap := s.detector.Scan(ctx)
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
	if c.Mode != device.ModeADB {
		return InstallTarget{}, profile.Profile{}, ErrNotADB
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
	state := InstallState{Target: s.installTarget, Installed: s.installed, Packages: stage.PackageInfos(s.packages),
		Choices: []stage.Plan{}, Problem: s.installProblem, Job: s.installJob}
	var p profile.Profile
	if state.Target != nil {
		for _, candidate := range s.profiles {
			if candidate.ID == state.Target.Profile {
				p = candidate
			}
		}
	}
	for _, choice := range []string{"full", "device", "relay"} {
		if state.Problem != nil || state.Installed == nil {
			problem := state.Problem
			if problem == nil {
				problem = &stage.Problem{Code: "no-device"}
			}
			state.Choices = append(state.Choices, stage.Plan{Choice: choice, Steps: []stage.Step{}, Problem: problem})
		} else {
			state.Choices = append(state.Choices, stage.BuildPlan(p, *state.Installed, s.packages, stage.Request{Choice: choice, Downgrade: true}))
		}
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

func (s *Service) inspectInstall(ctx context.Context) (profile.Profile, error) {
	target, p, err := s.installDevice(ctx)
	var facts stage.Facts
	if err == nil {
		s.mu.Lock()
		connector, packages := s.adb, slices.Clone(s.packages)
		s.mu.Unlock()
		var d adb.Device
		d, err = connector.Connect(ctx, target.Device.USB)
		if err == nil {
			facts, err = stage.Inspect(ctx, d.Shell, p, packages)
			d.Close()
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.installTarget, s.installed, s.installProblem = nil, nil, nil
	if err != nil {
		s.installProblem = installProblem(err)
	} else {
		s.installTarget, s.installed = &target, &facts
	}
	return p, err
}

// Install refreshes read-only device facts when the shared device lock is available. During a job
// its cached progress is returned immediately, so polling never opens a second adb connection.
func (s *Service) Install(ctx context.Context) InstallState {
	if s.configMu.TryLock() {
		readCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		_, _ = s.inspectInstall(readCtx)
		cancel()
		s.configMu.Unlock()
	}
	return s.emitInstall()
}

// InstallAdd loads and checks a local file. Loading another version replaces that layer's package.
func (s *Service) InstallAdd(filename string) (InstallState, error) {
	if !s.configMu.TryLock() {
		return s.InstallSnapshot(), ErrDeviceBusy
	}
	defer s.configMu.Unlock()
	pkg, err := stage.Open(filename)
	if err != nil {
		return s.InstallSnapshot(), err
	}
	s.mu.Lock()
	s.packages = slices.DeleteFunc(s.packages, func(p *stage.Package) bool { return p.Manifest().Stage == pkg.Manifest().Stage })
	s.packages = append(s.packages, pkg)
	// Space and required packages must be re-read for the newly loaded package's requirements.
	s.installed = nil
	s.mu.Unlock()
	return s.emitInstall(), nil
}

func (s *Service) InstallRemove(id string) (InstallState, error) {
	if !s.configMu.TryLock() {
		return s.InstallSnapshot(), ErrDeviceBusy
	}
	defer s.configMu.Unlock()
	s.mu.Lock()
	s.packages = slices.DeleteFunc(s.packages, func(p *stage.Package) bool { return p.Info().ID == id })
	s.installed = nil
	s.mu.Unlock()
	return s.emitInstall(), nil
}

// InstallStart re-plans against fresh facts under the same lock held until the job finishes. The
// request may end after this returns; the service lifetime, not the HTTP connection, owns the job.
func (s *Service) InstallStart(ctx context.Context, req stage.Request) (InstallState, error) {
	if !s.configMu.TryLock() {
		return s.InstallSnapshot(), ErrDeviceBusy
	}
	p, err := s.inspectInstall(ctx)
	if err != nil {
		s.configMu.Unlock()
		return s.emitInstall(), err
	}
	s.mu.Lock()
	packages, facts := slices.Clone(s.packages), *s.installed
	plan := stage.BuildPlan(p, facts, packages, req)
	if plan.Problem != nil {
		s.mu.Unlock()
		s.configMu.Unlock()
		return s.emitInstall(), plan.Problem
	}
	life := s.lifeCtx
	if life == nil {
		life = context.Background()
	}
	jobCtx, cancel := context.WithCancel(life)
	done := make(chan struct{})
	s.installCancel, s.installDone = cancel, done
	initial := stage.Job{Status: "running", Started: time.Now(), Failed: -1, Steps: []stage.StepProgress{}}
	for _, step := range plan.Steps {
		initial.Steps = append(initial.Steps, stage.StepProgress{Step: step, Status: "pending"})
	}
	s.installJob = &initial
	s.mu.Unlock()
	executor := stage.NewExecutor(p, packages)
	if s.newExecutor != nil {
		executor = s.newExecutor(p, packages)
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
		s.mu.Lock()
		connector := s.adb
		s.mu.Unlock()
		d, err := connector.Connect(ctx, t.Device.USB)
		if err == nil {
			s.mu.Lock()
			s.installTarget = &t
			s.mu.Unlock()
		}
		return d, err
	}
	executor.Emit = func(job stage.Job) {
		s.mu.Lock()
		s.installJob = &job
		if job.Installed != nil {
			s.installed = job.Installed
		}
		s.mu.Unlock()
		s.emitInstall()
	}
	state := s.emitInstall()
	go func() {
		defer close(done)
		defer cancel()
		defer s.configMu.Unlock()
		executor.Run(jobCtx, plan)
	}()
	return state, nil
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
		return ""
	}
	return s.installJob.Log
}
