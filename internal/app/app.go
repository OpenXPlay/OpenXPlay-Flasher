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

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/device"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/profile"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/usb"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/version"
)

// EventDevices carries a device.Snapshot each time the attached candidates change.
const EventDevices = "flasher:devices"

// Events receives the state the core pushes to a shell.
type Events interface {
	Emit(name string, data any)
}

// Info identifies the build, for the about view and diagnostics.
type Info struct {
	Name      string `json:"name"`
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	License   string `json:"license"`
	SourceURL string `json:"sourceUrl"`
}

// Service owns one flasher session.
type Service struct {
	profiles []profile.Profile
	detector device.Detector
	events   Events
	interval time.Duration

	mu      sync.Mutex
	current device.Snapshot
	cancel  context.CancelFunc
	done    chan struct{}
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
	s.done = make(chan struct{})
	go func() {
		defer close(s.done)
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
	s.cancel, s.done = nil, nil
	s.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
}

// Info returns the build identity.
func (s *Service) Info() Info {
	return Info{
		Name:      version.Name,
		Version:   version.Version,
		Commit:    version.Commit,
		License:   version.License,
		SourceURL: version.SourceURL,
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
