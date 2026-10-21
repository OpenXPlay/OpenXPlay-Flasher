// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-20

// Package app is the flasher core shared by the GUI and the CLI. It knows nothing about Wails or
// the terminal: shells call Service methods and receive pushed state through Events.
package app

import (
	"context"
	"sync"
	"time"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/adb"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/config"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/device"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/power"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/profile"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/stage"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/usb"
)

// EventDevices carries a device.Snapshot each time the attached candidates change.
const EventDevices = "flasher:devices"

// Events receives the state the core pushes to a shell.
type Events interface {
	Emit(name string, data any)
}

// Service owns one flasher session.
type Service struct {
	profiles []profile.Profile
	detector device.Detector
	events   Events
	interval time.Duration

	mu              sync.Mutex
	current         device.Snapshot
	cancel          context.CancelFunc
	done            chan struct{}
	adb             adb.Connector
	lifeCtx         context.Context
	packages        []*stage.Package
	packageProblems map[string]*stage.Problem
	installTarget   *InstallTarget
	installed       *stage.Facts
	installStale    bool
	sleepInhibitor  power.Inhibitor
	installProblem  *stage.Problem
	installJob      *stage.Job
	installCancel   context.CancelFunc
	installDone     chan struct{}
	newExecutor     func(profile.Profile, []*stage.Package) *stage.Executor

	// configMu serialises configuration operations and holds the device for an entire install.
	configMu sync.Mutex
	// newEditor lets tests replace the device-side defaults (backup folder, health wait).
	newEditor func(adb.Device) *config.Editor
}

// New returns a stopped service for the given profiles; Start begins watching for devices.
func New(profiles []profile.Profile, detector device.Detector, events Events) *Service {
	return &Service{profiles: profiles, detector: detector, events: events, interval: time.Second}
}

// NewForBus returns a service over a USB bus with the profiles built into the binary.
func NewForBus(bus usb.Bus, events Events) (*Service, error) {
	profiles, err := profile.Builtin()
	if err != nil {
		return nil, err
	}
	det := device.BusDetector{Bus: bus, Classifier: device.NewClassifier(profiles)}
	return New(profiles, det, events), nil
}

// Start watches for devices until Stop or until ctx is done. Calling Start twice is a no-op.
func (s *Service) Start(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		return
	}
	ctx, s.cancel = context.WithCancel(ctx)
	s.lifeCtx = ctx
	done := make(chan struct{})
	s.done = done
	go func() {
		defer close(done)
		device.Watch(ctx, s.detector, s.interval, func(snap device.Snapshot) {
			s.mu.Lock()
			s.current = snap
			s.mu.Unlock()
			s.events.Emit(EventDevices, snap)
		})
	}()
}

// Stop ends the watch and waits for it to return.
func (s *Service) Stop() {
	s.mu.Lock()
	cancel, done := s.cancel, s.done
	installCancel, installDone := s.installCancel, s.installDone
	s.cancel, s.done = nil, nil
	s.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
	if installCancel != nil {
		installCancel()
		<-installDone
	}
}

// Profiles returns the supported device models.
func (s *Service) Profiles() []profile.Profile {
	return s.profiles
}

// Devices returns the latest snapshot from the watch.
func (s *Service) Devices() device.Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.current
}

// Rescan scans now, without waiting for the next poll.
func (s *Service) Rescan(ctx context.Context) device.Snapshot {
	return s.detector.Scan(ctx)
}
