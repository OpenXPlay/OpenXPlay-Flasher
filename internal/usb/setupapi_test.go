// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-20

package usb

import (
	"reflect"
	"testing"
)

func TestParseInstanceID(t *testing.T) {
	cases := []struct {
		id              string
		vendor, product uint16
		iface           int
		ok              bool
	}{
		{`USB\VID_18D1&PID_D001\0123456789`, 0x18d1, 0xd001, -1, true},
		{`USB\VID_18D1&PID_D001&MI_01\7&2F3A1B&0&0001`, 0x18d1, 0xd001, 1, true},
		{`usb\vid_05c6&pid_9008\5&1c2d&0&2`, 0x05c6, 0x9008, -1, true},
		{`USB\ROOT_HUB30\4&2B8F0E5C&0&0`, 0, 0, 0, false},
	}
	for _, tc := range cases {
		v, p, i, ok := parseInstanceID(tc.id)
		if v != tc.vendor || p != tc.product || i != tc.iface || ok != tc.ok {
			t.Errorf("parseInstanceID(%q) = %04x %04x %d %v", tc.id, v, p, i, ok)
		}
	}
}

func TestInstanceSerial(t *testing.T) {
	cases := map[string]string{
		`USB\VID_18D1&PID_D001\0123456789`:            "0123456789",
		`USB\VID_18D1&PID_D001&MI_01\7&2F3A1B&0&0001`: "",
		`USB\VID_05C6&PID_9008\5&1C2D&0&2`:            "",
		`USB\ROOT_HUB30`:                              "",
	}
	for id, want := range cases {
		if got := instanceSerial(id); got != want {
			t.Errorf("instanceSerial(%q) = %q, want %q", id, got, want)
		}
	}
}

func TestParseClassAndLocation(t *testing.T) {
	c, ok := parseClass([]string{`USB\Class_ff&SubClass_42&Prot_01`, `USB\Class_ff&SubClass_42`, `USB\Class_ff`})
	if !ok || c != (Interface{Class: 0xff, SubClass: 0x42, Protocol: 0x01}) {
		t.Fatalf("parseClass = %+v %v", c, ok)
	}
	if _, ok := parseClass([]string{`USB\COMPOSITE`, `USB\DevClass_00&SubClass_00&Prot_00`}); ok {
		t.Fatal("composite parent reported a class")
	}
	bus, ports, ok := parseLocation([]string{`ACPI(_SB_)#ACPI(PCI0)#ACPI(XHC_)`, `PCIROOT(0)#PCI(1400)#USBROOT(0)#USB(2)#USB(1)#USBMI(1)`})
	if !ok || bus != 0 || !reflect.DeepEqual(ports, []int{2, 1}) {
		t.Fatalf("parseLocation = %d %v %v", bus, ports, ok)
	}
	if _, _, ok := parseLocation([]string{`PCIROOT(0)#PCI(1400)`}); ok {
		t.Fatal("non-USB path accepted")
	}
}

// A composite OpenStick gadget (RNDIS + adb) behind a hub, a single-interface fastboot device and
// a root hub, as SetupAPI lists them.
func TestDevicesFromNodes(t *testing.T) {
	nodes := []winNode{
		{InstanceID: `USB\ROOT_HUB30\4&2B8F0E5C&0&0`, LocationPaths: []string{`PCIROOT(0)#PCI(1400)#USBROOT(0)`}},
		{InstanceID: `USB\VID_18D1&PID_D001\0123456789`, CompatibleIDs: []string{`USB\DevClass_00&SubClass_00&Prot_00`, `USB\COMPOSITE`},
			LocationPaths: []string{`PCIROOT(0)#PCI(1400)#USBROOT(0)#USB(2)#USB(1)`}, Address: 1},
		{InstanceID: `USB\VID_18D1&PID_D001&MI_00\7&1&0&0000`, CompatibleIDs: []string{`USB\Class_e0&SubClass_01&Prot_03`},
			LocationPaths: []string{`PCIROOT(0)#PCI(1400)#USBROOT(0)#USB(2)#USB(1)#USBMI(0)`}},
		{InstanceID: `USB\VID_18D1&PID_D001&MI_02\7&1&0&0002`, CompatibleIDs: []string{`USB\Class_ff&SubClass_42&Prot_01`},
			LocationPaths: []string{`PCIROOT(0)#PCI(1400)#USBROOT(0)#USB(2)#USB(1)#USBMI(2)`}},
		{InstanceID: `USB\VID_18D1&PID_D00D\a191abc8`, CompatibleIDs: []string{`USB\Class_ff&SubClass_42&Prot_03`},
			LocationPaths: []string{`PCIROOT(0)#PCI(1400)#USBROOT(0)#USB(3)`}, Address: 3},
	}
	got := devicesFromNodes(nodes)
	want := []DeviceInfo{
		{VendorID: 0x18d1, ProductID: 0xd001, Bus: 0, Path: []int{2, 1}, Address: 1,
			Interfaces: []Interface{{0xe0, 0x01, 0x03}, {0xff, 0x42, 0x01}}, Serial: "0123456789"},
		{VendorID: 0x18d1, ProductID: 0xd00d, Bus: 0, Path: []int{3}, Address: 3,
			Interfaces: []Interface{{0xff, 0x42, 0x03}}, Serial: "a191abc8"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("devicesFromNodes:\n got %+v\nwant %+v", got, want)
	}
	if got[0].Location() != "0-2.1" {
		t.Fatalf("location %q", got[0].Location())
	}
}
