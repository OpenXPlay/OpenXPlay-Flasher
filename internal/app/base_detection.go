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
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/base"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/device"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/profile"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/stage"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/usb"
)

type stockDesignation struct {
	Profile  string
	Identity base.Identity
	USB      usb.DeviceInfo
}
type sessionDetector struct{ s *Service }

func (d sessionDetector) Scan(ctx context.Context) device.Snapshot { return d.s.scanDevices(ctx, true) }

type jobDetector struct{ s *Service }

func (d jobDetector) Scan(ctx context.Context) device.Snapshot { return d.s.scanDevices(ctx, false) }

func (s *Service) SetFastboot(open base.OpenFastboot, supported bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fastbootOpen = open
	s.fastbootSupported = supported
}
func (s *Service) scanDevices(ctx context.Context, probe bool) device.Snapshot {
	snap := s.detector.Scan(ctx)
	snap.Candidates = slices.Clone(snap.Candidates)
	s.mu.Lock()
	open, supported := s.fastbootOpen, s.fastbootSupported
	for i := range snap.Candidates {
		c := &snap.Candidates[i]
		if d, ok := s.designations[c.USB.Location()]; ok && c.Mode == device.ModeADB && c.USB.ID() == d.USB.ID() && (c.USB.Serial == "" || c.USB.Serial == d.Identity.Serial) {
			c.Mode = profile.ModeStock
			c.Profiles = []string{d.Profile}
			c.Serialno = d.Identity.Serial
		}
	}
	s.mu.Unlock()
	// A running job owns the device lock; the watcher must never open a second transport.
	if !probe || !supported || open == nil || !s.configMu.TryLock() {
		return snap
	}
	defer s.configMu.Unlock()
	for i := range snap.Candidates {
		c := &snap.Candidates[i]
		if c.Mode != device.ModeFastboot {
			continue
		}
		readCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		fb, err := open(readCtx, c.USB)
		if err == nil {
			var identity base.Identity
			identity, err = base.ReadFastbootIdentity(readCtx, fb)
			fb.Close()
			if err == nil {
				c.Product, c.Serialno = identity.Product, identity.Serial
			}
		}
		if err != nil {
			c.ReadError = err.Error()
		}
		cancel()
	}
	return snap
}

// DesignateStock is the only entry point that opens an ignored adb device. A failed check
// forgets the designation and returns the observed model/release properties for review.
func (s *Service) DesignateStock(ctx context.Context, location string) (InstallState, error) {
	if !s.configMu.TryLock() {
		return s.InstallSnapshot(), ErrDeviceBusy
	}
	defer s.configMu.Unlock()
	snap := s.detector.Scan(ctx)
	if snap.Error != "" {
		return s.InstallSnapshot(), &stage.Problem{Code: "usb", Detail: snap.Error}
	}
	var target *device.Candidate
	for _, c := range snap.Candidates {
		if c.USB.Location() == location && c.Mode == device.ModeADB {
			if target != nil {
				return s.InstallSnapshot(), ErrSeveralDevices
			}
			copy := c
			target = &copy
		}
	}
	if target == nil {
		return s.InstallSnapshot(), ErrNoDevice
	}
	var p profile.Profile
	for _, candidate := range s.profiles {
		if candidate.ID == "ufi003" {
			p = candidate
		}
	}
	s.mu.Lock()
	connector := s.adb
	delete(s.designations, location)
	s.mu.Unlock()
	if connector == nil {
		return s.InstallSnapshot(), ErrNoADB
	}
	d, err := connector.Connect(ctx, target.USB)
	if err != nil {
		return s.InstallSnapshot(), err
	}
	identity, err := base.StockIdentity(ctx, d, p, adb.TransportSerial(d, target.USB.Serial))
	d.Close()
	if err != nil {
		return s.InstallSnapshot(), err
	}
	identity.Location = location
	s.mu.Lock()
	s.designations[location] = stockDesignation{Profile: p.ID, Identity: identity, USB: target.USB}
	s.mu.Unlock()
	_, err = s.inspectInstall(ctx, "")
	return s.emitInstall(), err
}

// DesignateStockSerial resolves only the explicitly named server/USB transport. It never tries
// commands on other unknown adb devices to find the serial.
func (s *Service) DesignateStockSerial(ctx context.Context, serial string) (InstallState, error) {
	snap := s.detector.Scan(ctx)
	var locations []string
	var devices []usb.DeviceInfo
	for _, c := range snap.Candidates {
		if c.Mode == device.ModeADB {
			devices = append(devices, c.USB)
			if c.USB.Serial == serial {
				locations = append(locations, c.USB.Location())
			}
		}
	}
	if len(locations) == 0 {
		s.mu.Lock()
		connector := s.adb
		s.mu.Unlock()
		if locator, ok := connector.(interface {
			LocateSerial(context.Context, string, []usb.DeviceInfo) (usb.DeviceInfo, error)
		}); ok {
			dev, err := locator.LocateSerial(ctx, serial, devices)
			if err != nil {
				return s.InstallSnapshot(), err
			}
			locations = append(locations, dev.Location())
		}
	}
	if len(locations) != 1 {
		return s.InstallSnapshot(), &stage.Problem{Code: "stock-location", Detail: serial}
	}
	state, err := s.DesignateStock(ctx, locations[0])
	if err != nil {
		return state, err
	}
	if state.Identity == nil || state.Identity.Serial != serial {
		s.mu.Lock()
		delete(s.designations, locations[0])
		s.mu.Unlock()
		return s.InstallSnapshot(), &stage.Problem{Code: "serial-mismatch"}
	}
	return state, nil
}

func (s *Service) loadJournal(ctx context.Context, p profile.Profile, serial string) error {
	s.mu.Lock()
	dir := s.dataDir
	s.mu.Unlock()
	if dir == "" {
		return nil
	}
	j, err := (base.Journals{Dir: dir}).Load(ctx, p.ID, serial)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.unfinished = j
	if err != nil {
		s.installProblem = installProblem(err)
	}
	return err
}
func (s *Service) inspectBaseTarget(ctx context.Context) (bool, profile.Profile, error) {
	snap := s.scanDevices(ctx, false)
	var chosen *device.Candidate
	for _, c := range snap.Candidates {
		if c.Mode == profile.ModeFastboot || c.Mode == profile.ModeStock {
			copy := c
			chosen = &copy
			break
		}
	}
	if chosen == nil {
		return false, profile.Profile{}, nil
	}
	c, err := base.OneCandidate(snap, chosen.Mode)
	var p profile.Profile
	var identity base.Identity
	s.mu.Lock()
	open, supported := s.fastbootOpen, s.fastbootSupported
	connector := s.adb
	s.mu.Unlock()
	if err == nil && c.Mode == profile.ModeFastboot {
		if !supported || open == nil {
			err = &stage.Problem{Code: "fastboot-unsupported"}
		} else {
			fb, openErr := open(ctx, c.USB)
			err = openErr
			if err == nil {
				identity, err = base.ReadFastbootIdentity(ctx, fb)
				fb.Close()
			}
		}
		for _, candidate := range s.profiles {
			if candidate.Base != nil && (identity.Product == candidate.Base.Fastboot.Product || identity.Product == candidate.Base.Fastboot.StockProduct || identity.Product == "" && slices.Contains(c.Profiles, candidate.ID)) {
				if p.ID != "" {
					err = ErrUnconfirmed
					break
				}
				p = candidate
			}
		}
		if p.ID == "" && err == nil {
			err = &stage.Problem{Code: "wrong-product", Detail: identity.Product}
		}
	} else if err == nil {
		s.mu.Lock()
		designated := s.designations[c.USB.Location()]
		s.mu.Unlock()
		for _, candidate := range s.profiles {
			if candidate.ID == designated.Profile {
				p = candidate
			}
		}
		if connector == nil {
			err = ErrNoADB
		} else {
			d, openErr := connector.Connect(ctx, c.USB)
			err = openErr
			if err == nil {
				identity, err = base.StockIdentity(ctx, d, p, adb.TransportSerial(d, c.USB.Serial))
				d.Close()
			}
		}
		if err != nil {
			s.mu.Lock()
			delete(s.designations, c.USB.Location())
			s.mu.Unlock()
		}
	}
	if err != nil {
		s.mu.Lock()
		s.installProblem = installProblem(err)
		s.installStale = s.installed != nil
		// Even an unsupported transfer backend can describe choices for a USB-matched profile.
		if p.ID != "" && errors.Is(err, usb.ErrTransfersUnsupported) {
			s.fastbootSupported = false
		}
		if p.ID != "" && c != nil {
			t := InstallTarget{Device: *c, Profile: p.ID}
			s.installTarget = &t
			s.installed = &stage.Facts{Layers: map[string]string{}}
			s.baseIdentity = nil
		}
		s.mu.Unlock()
		return true, p, err
	}
	identity.Location = c.USB.Location()
	c.Product, c.Serialno = identity.Product, identity.Serial
	s.mu.Lock()
	s.baseIdentity = &identity
	s.installTarget = &InstallTarget{Device: *c, Profile: p.ID}
	s.installed = &stage.Facts{Layers: map[string]string{}, Packages: map[string]string{}, FreeKiB: map[string]int64{}}
	s.installProblem = nil
	s.installStale = false
	s.mu.Unlock()
	err = s.loadJournal(ctx, p, identity.Serial)
	return true, p, err
}

func (s *Service) FastbootGetVar(ctx context.Context, name string) (string, error) {
	if !s.configMu.TryLock() {
		return "", ErrDeviceBusy
	}
	defer s.configMu.Unlock()
	c, err := base.OneCandidate(s.scanDevices(ctx, false), profile.ModeFastboot)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	open, supported := s.fastbootOpen, s.fastbootSupported
	s.mu.Unlock()
	if !supported || open == nil {
		return "", &stage.Problem{Code: "fastboot-unsupported"}
	}
	fb, err := open(ctx, c.USB)
	if err != nil {
		return "", err
	}
	defer fb.Close()
	return fb.GetVar(ctx, name)
}
