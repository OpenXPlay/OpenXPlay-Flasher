// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package app

import (
	"context"
	"errors"
	"io/fs"
	"strings"
	"testing"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/adb"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/device"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/usb"
)

// cannedDevice answers the editor's probe for one file and serves its content.
type cannedDevice struct {
	path, content string
	closed        bool
}

func (d *cannedDevice) Shell(_ context.Context, cmd string) (adb.Result, error) {
	out := "file\n" + d.path + "\n644\nroot:root\n" + "13\n"
	return adb.Result{Stdout: []byte(out)}, nil
}

func (d *cannedDevice) Pull(context.Context, string, int64) ([]byte, error) {
	return []byte(d.content), nil
}

func (d *cannedDevice) Push(context.Context, string, []byte, fs.FileMode) error { return nil }

func (d *cannedDevice) Close() error {
	d.closed = true
	return nil
}

type fakeConnector struct {
	dev *cannedDevice
	got usb.DeviceInfo
}

func (c *fakeConnector) Connect(_ context.Context, d usb.DeviceInfo) (adb.Device, error) {
	c.got = d
	return c.dev, nil
}

var (
	ufi003ADB = device.Candidate{Mode: device.ModeADB, Profiles: []string{"ufi003"},
		USB: usb.DeviceInfo{VendorID: 0x18d1, ProductID: 0xd001, Bus: 0, Path: []int{1, 1, 1}}}
	phone = device.Candidate{Mode: device.ModeADB, Profiles: []string{}, USB: usb.DeviceInfo{VendorID: 0x2717, ProductID: 0xff08}}
)

func serviceWith(t *testing.T, candidates ...device.Candidate) *Service {
	t.Helper()
	s, err := NewForBus(usb.NewFakeBus(), &recorder{})
	if err != nil {
		t.Fatal(err)
	}
	s.current = device.Snapshot{Candidates: candidates}
	return s
}

func TestConfigTargetNeedsOneLinuxDevice(t *testing.T) {
	s := serviceWith(t, ufi003ADB, phone)
	if _, err := s.ConfigTarget(); !errors.Is(err, ErrNoADB) {
		t.Fatalf("without adb: %v", err)
	}
	s.SetADB(&fakeConnector{})
	target, err := s.ConfigTarget()
	if err != nil || target.Profile != "ufi003" || target.Service != "openxplay-relay.service" || len(target.Files) != 2 {
		t.Fatalf("ConfigTarget = %+v, %v", target, err)
	}
	cases := map[string]struct {
		candidates []device.Candidate
		want       error
	}{
		"only a phone":      {[]device.Candidate{phone}, ErrNoDevice},
		"two dongles":       {[]device.Candidate{ufi003ADB, ufi003ADB}, ErrSeveralDevices},
		"fastboot":          {[]device.Candidate{{Mode: device.ModeFastboot, Profiles: []string{"ufi003"}}}, ErrNotADB},
		"family, not model": {[]device.Candidate{{Mode: device.ModeADB, Profiles: []string{"ufi001", "ufi003"}}}, ErrUnconfirmed},
	}
	for name, c := range cases {
		s.current = device.Snapshot{Candidates: c.candidates}
		if _, err := s.ConfigTarget(); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
	}
}

func TestWritePolicy(t *testing.T) {
	s := serviceWith(t, ufi003ADB)
	s.SetADB(&fakeConnector{})
	target, _ := s.ConfigTarget()
	if f, svc := writePolicy(target, "/var/lib/openxplay//settings.json", false); f != "json" || svc != "openxplay-relay.service" {
		t.Errorf("listed file: %q %q", f, svc)
	}
	if f, svc := writePolicy(target, "/etc/hostname", false); f != "text" || svc != "" {
		t.Errorf("other file without restart: %q %q", f, svc)
	}
	if _, svc := writePolicy(target, "/etc/hostname", true); svc != "openxplay-relay.service" {
		t.Errorf("other file with restart: %q", svc)
	}
}

func TestConfigReadUsesTheDongle(t *testing.T) {
	s := serviceWith(t, phone, ufi003ADB)
	dev := &cannedDevice{path: "/etc/openxplay/host.json", content: `{"model":"x"}`}
	conn := &fakeConnector{dev: dev}
	s.SetADB(conn)
	f, err := s.ConfigRead(context.Background(), "/etc/openxplay/host.json")
	if err != nil || !f.Exists || f.Content != `{"model":"x"}` || f.Owner != "root:root" {
		t.Fatalf("ConfigRead = %+v, %v", f, err)
	}
	if conn.got.Location() != "0-1.1.1" || !dev.closed {
		t.Fatalf("connected to %s, closed %v", conn.got.Location(), dev.closed)
	}
	if _, err := s.ConfigRead(context.Background(), "/var/lib/openxplay/mfi/identity.pk8"); err == nil || !strings.Contains(err.Error(), "protected") {
		t.Fatalf("protected read: %v", err)
	}
}
