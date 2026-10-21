// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

//go:build cgo && !windows

package usb

import (
	"errors"
	"fmt"
	"testing"

	"github.com/google/gousb"
)

func TestLibusbErr(t *testing.T) {
	// gousb wraps claim errors as text.
	claim := fmt.Errorf("failed to claim interface 2 on dev: %v", gousb.ErrorAccess)
	if err := libusbErr(claim, true); !errors.Is(err, ErrBusy) {
		t.Errorf("claim access: %v", err)
	}
	if err := libusbErr(gousb.ErrorBusy, true); !errors.Is(err, ErrBusy) {
		t.Errorf("claim busy: %v", err)
	}
	// Opening without permission is not the device being in use.
	if err := libusbErr(gousb.ErrorAccess, false); errors.Is(err, ErrBusy) {
		t.Errorf("open access: %v", err)
	}
	if err := libusbErr(gousb.ErrorNoDevice, false); !errors.Is(err, ErrNotFound) {
		t.Errorf("no device: %v", err)
	}
}

// The UFI003 in adb mode: RNDIS control and data interfaces, then adb.
func TestFindBulk(t *testing.T) {
	ep := func(addr gousb.EndpointAddress, typ gousb.TransferType, packet int) gousb.EndpointDesc {
		return gousb.EndpointDesc{Address: addr, Number: int(addr & 0x0f), Direction: addr&0x80 != 0,
			TransferType: typ, MaxPacketSize: packet}
	}
	cfg := gousb.ConfigDesc{Number: 1, Interfaces: []gousb.InterfaceDesc{
		{Number: 0, AltSettings: []gousb.InterfaceSetting{{Number: 0, Class: 2, SubClass: 2, Protocol: 0xff,
			Endpoints: map[gousb.EndpointAddress]gousb.EndpointDesc{0x83: ep(0x83, gousb.TransferTypeInterrupt, 8)}}}},
		{Number: 1, AltSettings: []gousb.InterfaceSetting{{Number: 1, Class: 10,
			Endpoints: map[gousb.EndpointAddress]gousb.EndpointDesc{0x81: ep(0x81, gousb.TransferTypeBulk, 512), 0x01: ep(0x01, gousb.TransferTypeBulk, 512)}}}},
		{Number: 2, AltSettings: []gousb.InterfaceSetting{{Number: 2, Class: 0xff, SubClass: 0x42, Protocol: 1,
			Endpoints: map[gousb.EndpointAddress]gousb.EndpointDesc{0x82: ep(0x82, gousb.TransferTypeBulk, 512), 0x02: ep(0x02, gousb.TransferTypeBulk, 512)}}}},
	}}
	setting, in, out, ok := findBulk(cfg, Interface{Class: 0xff, SubClass: 0x42, Protocol: 1})
	if !ok || setting.Number != 2 || in.Address != 0x82 || out.Address != 0x02 {
		t.Fatalf("findBulk = %d %s %s %v", setting.Number, in.Address, out.Address, ok)
	}
	if _, _, _, ok := findBulk(cfg, Interface{Class: 0xff, SubClass: 0x42, Protocol: 3}); ok {
		t.Fatal("found a fastboot interface")
	}
	if _, _, _, ok := findBulk(cfg, Interface{Class: 2, SubClass: 2, Protocol: 0xff}); ok {
		t.Fatal("found bulk endpoints on an interrupt-only interface")
	}
}
