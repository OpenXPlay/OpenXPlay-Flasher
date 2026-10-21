// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-20

package device

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/profile"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/usb"
)

// A second model of the same family shares the adb IDs but has its own fastboot ID.
var sibling = profile.Profile{ID: "ufi001", Family: "openstick-msm8916",
	USB: []profile.USBMatch{{Mode: profile.ModeADB, VendorID: "18d1", ProductID: "d001"},
		{Mode: profile.ModeFastboot, VendorID: "18d1", ProductID: "d0ff"}}}

func classifier(t *testing.T) Classifier {
	t.Helper()
	builtin, err := profile.Builtin()
	if err != nil {
		t.Fatal(err)
	}
	return NewClassifier(append(builtin, sibling))
}

var (
	openStick = usb.DeviceInfo{VendorID: 0x18d1, ProductID: 0xd001, Bus: 1, Address: 7, Path: []int{2},
		Interfaces: []usb.Interface{{Class: 0xe0, SubClass: 0x01, Protocol: 0x03}, {Class: 0xff, SubClass: 0x42, Protocol: 0x01}}}
	fastboot = usb.DeviceInfo{VendorID: 0x18d1, ProductID: 0xd00d, Bus: 1, Address: 8, Path: []int{2},
		Interfaces: []usb.Interface{{Class: 0xff, SubClass: 0x42, Protocol: 0x03}}}
	edl      = usb.DeviceInfo{VendorID: 0x05c6, ProductID: 0x9008, Bus: 1, Address: 9, Path: []int{2}}
	phoneADB = usb.DeviceInfo{VendorID: 0x2717, ProductID: 0xff48, Bus: 2, Address: 3, Path: []int{1},
		Interfaces: []usb.Interface{{Class: 0xff, SubClass: 0x42, Protocol: 0x01}}}
	keyboard = usb.DeviceInfo{VendorID: 0x05ac, ProductID: 0x0250, Bus: 2, Address: 4, Path: []int{3},
		Interfaces: []usb.Interface{{Class: 0x03, SubClass: 0x01, Protocol: 0x01}}}
)

func TestClassify(t *testing.T) {
	unknownFastboot := usb.DeviceInfo{VendorID: 0x2717, ProductID: 0x0001,
		Interfaces: []usb.Interface{{Class: 0xff, SubClass: 0x42, Protocol: 0x03}}}
	cases := []struct {
		name     string
		dev      usb.DeviceInfo
		ok       bool
		mode     Mode
		profiles string
	}{
		{"openstick adb matches the whole family", openStick, true, ModeADB, "ufi001,ufi003"},
		{"lk fastboot", fastboot, true, ModeFastboot, "ufi003"},
		{"edl", edl, true, ModeEDL, "ufi003"},
		{"other adb device", phoneADB, true, ModeADB, ""},
		{"other fastboot device", unknownFastboot, true, ModeFastboot, ""},
		{"keyboard", keyboard, false, "", ""},
	}
	cl := classifier(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, ok := cl.Classify(tc.dev)
			if ok != tc.ok || c.Mode != tc.mode || strings.Join(c.Profiles, ",") != tc.profiles {
				t.Fatalf("Classify = %+v, %v; want mode %q profiles %q ok %v", c, ok, tc.mode, tc.profiles, tc.ok)
			}
		})
	}
}

func TestBusDetectorScan(t *testing.T) {
	bus := usb.NewFakeBus(keyboard, phoneADB, openStick)
	det := BusDetector{Bus: bus, Classifier: classifier(t)}
	snap := det.Scan(context.Background())
	if len(snap.Candidates) != 2 || snap.Candidates[0].USB.ID() != "18d1:d001" || snap.Candidates[1].USB.ID() != "2717:ff48" {
		t.Fatalf("unexpected snapshot %+v", snap)
	}
	bus.Fail(errors.New("libusb: access denied"))
	if snap := det.Scan(context.Background()); snap.Error == "" || len(snap.Candidates) != 0 {
		t.Fatalf("want an error snapshot, got %+v", snap)
	}
}

func TestWatchReportsChangesOnly(t *testing.T) {
	bus := usb.NewFakeBus(openStick)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	got := make(chan Snapshot, 8)
	go Watch(ctx, BusDetector{Bus: bus, Classifier: classifier(t)}, 5*time.Millisecond, func(s Snapshot) { got <- s })

	expect := func(want string) {
		t.Helper()
		select {
		case s := <-got:
			ids := ""
			for _, c := range s.Candidates {
				ids += string(c.Mode) + " "
			}
			if ids != want {
				t.Fatalf("snapshot modes %q, want %q", ids, want)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("no snapshot, want %q", want)
		}
	}
	expect("adb ")
	select {
	case s := <-got:
		t.Fatalf("unchanged bus reported again: %+v", s)
	case <-time.After(30 * time.Millisecond):
	}
	bus.Set(fastboot)
	expect("fastboot ")
	bus.Set()
	expect("")
}

func TestN1RequiresNCMInterface(t *testing.T) {
	profiles, err := profile.Builtin()
	if err != nil {
		t.Fatal(err)
	}
	cl := NewClassifier(profiles)
	d := usb.DeviceInfo{VendorID: 0x1d6b, ProductID: 0x0104}
	if _, ok := cl.Classify(d); ok {
		t.Fatal("generic gadget matched N1 without NCM")
	}
	d.Interfaces = []usb.Interface{{Class: 2, SubClass: 13, Protocol: 0}}
	c, ok := cl.Classify(d)
	if !ok || c.Mode != ModeSSH || len(c.Profiles) != 1 || c.Profiles[0] != "phicomm-n1" {
		t.Fatalf("%+v", c)
	}
}
