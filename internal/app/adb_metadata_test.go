// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-22

package app

import (
	"context"
	"testing"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/adb"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/device"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/usb"
)

type metadataConnector struct {
	t        *testing.T
	metadata map[string]adb.Metadata
}

func (c *metadataConnector) Connect(context.Context, usb.DeviceInfo) (adb.Device, error) {
	c.t.Fatal("ignored adb device was opened")
	return nil, adb.ErrNoDevice
}
func (c *metadataConnector) Describe(context.Context, []usb.DeviceInfo) map[string]adb.Metadata {
	return c.metadata
}

func TestIgnoredADBMetadataIsDisplayOnly(t *testing.T) {
	stock := usb.DeviceInfo{VendorID: 0x05c6, ProductID: 0x90b4, Bus: 0, Address: 8, Path: []int{1, 1, 1},
		Interfaces: []usb.Interface{{Class: 0xff, SubClass: 0x42, Protocol: 0x01}}}
	phone := stock
	phone.VendorID, phone.ProductID, phone.Address, phone.Path, phone.Serial = 0x2717, 0xff08, 9, []int{2}, "phone-serial"
	s, err := NewForBus(usb.NewFakeBus(stock, phone), nil)
	if err != nil {
		t.Fatal(err)
	}
	c := &metadataConnector{t: t, metadata: map[string]adb.Metadata{
		stock.Location(): {Serial: "4336bd41", Model: "msm8916_32_512"},
	}}
	s.SetADB(c)
	snap := s.Rescan(t.Context())
	a, b := snap.Candidates[0], snap.Candidates[1]
	if a.ADBName != "msm8916_32_512" || a.ADBSerial != "4336bd41" || b.ADBName != "" || b.ADBSerial != phone.Serial {
		t.Fatalf("labels = %+v", snap.Candidates)
	}
	for _, candidate := range snap.Candidates {
		if candidate.Mode != device.ModeADB || candidate.Supported() || candidate.Serialno != "" || candidate.Product != "" {
			t.Fatalf("metadata established an identity: %+v", candidate)
		}
	}
	if state := s.Install(t.Context()); state.Target != nil {
		t.Fatal("advertised model selected an ignored device")
	}
	c.metadata[stock.Location()] = adb.Metadata{Serial: "other", Product: "product-fallback"}
	next := s.Rescan(t.Context())
	if next.Equal(snap) || next.Candidates[0].ADBName != "product-fallback" || next.Candidates[0].ADBSerial != "other" {
		t.Fatalf("metadata change was not visible: %+v", next)
	}
	c.metadata = nil
	if next := s.Rescan(t.Context()); next.Candidates[0].ADBName != "" || next.Candidates[0].ADBSerial != "" {
		t.Fatalf("unavailable metadata kept a stale label: %+v", next)
	}
}
