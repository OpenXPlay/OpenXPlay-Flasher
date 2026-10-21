// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-20

// Package device finds supported devices on the USB bus and names the mode each one is in. USB
// descriptors tell the transport and which profiles may match; the model and what is installed on
// it are confirmed over adb or fastboot.
package device

import (
	"context"
	"slices"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/profile"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/usb"
)

// Mode is the USB transport a candidate offers.
type Mode = profile.Mode

const (
	ModeADB      = profile.ModeADB
	ModeFastboot = profile.ModeFastboot
	ModeEDL      = profile.ModeEDL
)

// Generic signatures for devices no profile names: the Android vendor class interfaces (ff/42/xx)
// and Qualcomm's emergency download mode.
var (
	adbInterface      = usb.Interface{Class: 0xff, SubClass: 0x42, Protocol: 0x01}
	fastbootInterface = usb.Interface{Class: 0xff, SubClass: 0x42, Protocol: 0x03}
)

const (
	qualcommVendor = 0x05c6
	edlProduct     = 0x9008
)

// Candidate is a device that may be one the flasher supports.
type Candidate struct {
	Mode Mode           `json:"mode"`
	USB  usb.DeviceInfo `json:"usb"`
	// Profiles lists the IDs of the profiles whose USB signature matches, sorted. Several match when
	// models share IDs (an OpenStick family); none match for an unknown adb or fastboot device,
	// such as a phone.
	Profiles []string `json:"profiles"`
}

// Supported reports whether a profile matches the candidate.
func (c Candidate) Supported() bool { return len(c.Profiles) > 0 }

// Classifier matches USB devices against profiles.
type Classifier struct {
	profiles []profile.Profile
}

func NewClassifier(profiles []profile.Profile) Classifier {
	return Classifier{profiles: profiles}
}

// Classify returns the candidate for a device, or false when it is neither matched by a profile
// nor an adb, fastboot or EDL device.
func (cl Classifier) Classify(d usb.DeviceInfo) (Candidate, bool) {
	c := Candidate{USB: d, Profiles: []string{}}
	for _, p := range cl.profiles {
		for _, m := range p.USB {
			vid, pid, err := m.IDs()
			if err != nil || vid != d.VendorID || pid != d.ProductID {
				continue
			}
			if c.Mode == "" {
				c.Mode = m.Mode
			}
			if m.Mode == c.Mode && !slices.Contains(c.Profiles, p.ID) {
				c.Profiles = append(c.Profiles, p.ID)
			}
		}
	}
	if c.Mode == "" {
		switch {
		case d.VendorID == qualcommVendor && d.ProductID == edlProduct:
			c.Mode = ModeEDL
		case d.HasInterface(fastbootInterface):
			c.Mode = ModeFastboot
		case d.HasInterface(adbInterface):
			c.Mode = ModeADB
		default:
			return Candidate{}, false
		}
	}
	slices.Sort(c.Profiles)
	return c, true
}

// Snapshot is the set of candidates found by one scan.
type Snapshot struct {
	Candidates []Candidate `json:"candidates"`
	// Error is the last bus error, empty when the scan succeeded.
	Error string `json:"error,omitempty"`
}

// Equal reports whether two snapshots show the same devices and error.
func (s Snapshot) Equal(o Snapshot) bool {
	return s.Error == o.Error && slices.EqualFunc(s.Candidates, o.Candidates, func(a, b Candidate) bool {
		return a.Mode == b.Mode && slices.Equal(a.Profiles, b.Profiles) && a.USB.ID() == b.USB.ID() &&
			a.USB.Location() == b.USB.Location() && a.USB.Address == b.USB.Address
	})
}

// Detector scans for candidates.
type Detector interface {
	Scan(ctx context.Context) Snapshot
}

// BusDetector classifies every device on a usb.Bus.
type BusDetector struct {
	Bus        usb.Bus
	Classifier Classifier
}

func (d BusDetector) Scan(ctx context.Context) Snapshot {
	devices, err := d.Bus.List(ctx)
	if err != nil {
		return Snapshot{Error: err.Error()}
	}
	snap := Snapshot{Candidates: []Candidate{}}
	for _, dev := range devices {
		if c, ok := d.Classifier.Classify(dev); ok {
			snap.Candidates = append(snap.Candidates, c)
		}
	}
	slices.SortFunc(snap.Candidates, func(a, b Candidate) int {
		if a.USB.Bus != b.USB.Bus {
			return a.USB.Bus - b.USB.Bus
		}
		return a.USB.Address - b.USB.Address
	})
	return snap
}
