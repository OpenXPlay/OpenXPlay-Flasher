// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package app

import (
	"context"
	"errors"
	"slices"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/adb"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/config"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/device"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/profile"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/stage"
)

// Why the configuration window cannot work on a device right now.
var (
	ErrNoADB           = errors.New("config: this build cannot reach devices over adb")
	ErrNoDevice        = errors.New("config: no supported device is connected")
	ErrSeveralDevices  = errors.New("config: several supported devices are connected")
	ErrNotADB          = errors.New("config: the device is not running Linux with adb")
	ErrUnconfirmed     = errors.New("config: the device model is not confirmed")
	ErrNoConfigProfile = errors.New("config: this device model has no configuration files")
)

// ConfigTarget is the device the configuration window works on.
type ConfigTarget struct {
	Device  device.Candidate     `json:"device"`
	Profile string               `json:"profile"`
	Service string               `json:"service"`
	Files   []profile.ConfigFile `json:"files"`
}

// SetADB gives the service its way to reach devices over adb; without it the configuration
// window reports ErrNoADB.
func (s *Service) SetADB(c adb.Connector) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.adb = adb.Share(c)
}

// ConfigTarget picks the one supported device that runs Linux with adb.
func (s *Service) ConfigTarget() (ConfigTarget, error) {
	s.mu.Lock()
	snap, connector := s.current, s.adb
	s.mu.Unlock()
	snap = s.selectedSnapshot(s.addSSH(snap))
	if connector == nil && s.Connection().Device == nil {
		return ConfigTarget{}, ErrNoADB
	}
	var supported []device.Candidate
	for _, c := range snap.Candidates {
		if c.Supported() {
			supported = append(supported, c)
		}
	}
	switch {
	case len(supported) == 0:
		return ConfigTarget{}, ErrNoDevice
	case len(supported) > 1:
		return ConfigTarget{}, ErrSeveralDevices
	}
	c := supported[0]
	if c.Mode != device.ModeADB && c.Mode != device.ModeSSH {
		return ConfigTarget{}, ErrNotADB
	}
	if c.Mode == device.ModeSSH && s.Connection().Device == nil {
		return ConfigTarget{}, &stage.Problem{Code: "ssh-login"}
	}
	if len(c.Profiles) != 1 {
		return ConfigTarget{}, ErrUnconfirmed
	}
	i := slices.IndexFunc(s.profiles, func(p profile.Profile) bool { return p.ID == c.Profiles[0] })
	if i < 0 || len(s.profiles[i].Config) == 0 {
		return ConfigTarget{}, ErrNoConfigProfile
	}
	p := s.profiles[i]
	return ConfigTarget{Device: c, Profile: p.ID, Service: p.Service, Files: p.Config}, nil
}

// withEditor connects to the target device for one operation; operations never overlap.
func (s *Service) withEditor(ctx context.Context, fn func(*config.Editor, ConfigTarget) error) error {
	if !s.configMu.TryLock() {
		return ErrDeviceBusy
	}
	defer s.configMu.Unlock()
	t, err := s.ConfigTarget()
	if err != nil {
		return err
	}
	dev, err := s.openDevice(ctx, t.Device)
	if err != nil {
		return err
	}
	defer dev.Close()
	ed := config.NewEditor(dev)
	if s.newEditor != nil {
		ed = s.newEditor(dev)
	}
	return fn(ed, t)
}

// writePolicy is how a path is checked and whether the relay restarts after it changes: a file
// the profile lists brings its own format and restart rule; any other file is text and restarts
// the relay only when asked.
func writePolicy(t ConfigTarget, p string, restart bool) (format, service string) {
	format = "text"
	if clean, err := config.CheckPath(p); err == nil {
		p = clean
	}
	for _, f := range t.Files {
		if f.Path == p {
			format, restart = f.Format, f.RestartService
		}
	}
	if restart {
		service = t.Service
	}
	return format, service
}

// ConfigRead reads a file from the device.
func (s *Service) ConfigRead(ctx context.Context, p string) (config.File, error) {
	var f config.File
	err := s.withEditor(ctx, func(ed *config.Editor, _ ConfigTarget) (err error) {
		f, err = ed.Read(ctx, p)
		return err
	})
	return f, err
}

// ConfigWrite writes a file to the device; restart applies to files the profile does not list.
func (s *Service) ConfigWrite(ctx context.Context, p, content string, restart bool) (config.WriteResult, error) {
	var res config.WriteResult
	err := s.withEditor(ctx, func(ed *config.Editor, t ConfigTarget) (err error) {
		format, service := writePolicy(t, p, restart)
		res, err = ed.Write(ctx, config.WriteRequest{Path: p, Content: content, Format: format, Service: service})
		return err
	})
	return res, err
}

// ConfigBackups lists the kept versions of a file on the device.
func (s *Service) ConfigBackups(ctx context.Context, p string) ([]config.Backup, error) {
	var list []config.Backup
	err := s.withEditor(ctx, func(ed *config.Editor, _ ConfigTarget) (err error) {
		list, err = ed.Backups(ctx, p)
		return err
	})
	return list, err
}

// ConfigRestore puts a kept version back.
func (s *Service) ConfigRestore(ctx context.Context, p, id string, restart bool) (config.WriteResult, error) {
	var res config.WriteResult
	err := s.withEditor(ctx, func(ed *config.Editor, t ConfigTarget) (err error) {
		_, service := writePolicy(t, p, restart)
		res, err = ed.Restore(ctx, p, id, service)
		return err
	})
	return res, err
}
