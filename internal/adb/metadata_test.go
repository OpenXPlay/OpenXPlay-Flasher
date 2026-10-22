// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-22

package adb

import (
	"context"
	"reflect"
	"testing"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/usb"
)

func TestServerMetadataUsesOnlyHostInventory(t *testing.T) {
	s := newFakeServer(t, "4336bd41 device usb:0-1.1.1 product:msm8916 model:msm8916_32_512 device:msm8916 transport_id:1\n"+
		"4336bd41 device usb:0-2 model:Phone transport_id:2\n"+
		"locked unauthorized usb:0-3 transport_id:3\n"+
		"windows device product:win-product device:win-device transport_id:4\n")
	windows := at(1, 4)
	windows.Serial = "windows"
	ambiguous := at(0, 5)
	ambiguous.Serial = "4336bd41"
	devices := []usb.DeviceInfo{at(0, 1, 1, 1), at(0, 2), at(0, 3), windows, ambiguous, at(0, 6)}
	c := &AutoConnector{Server: ServerConnector{Addr: s.addr()}, USB: USBConnector{
		Open: func(context.Context, usb.DeviceInfo, usb.Interface) (usb.Bulk, error) {
			t.Fatal("metadata lookup opened USB")
			return nil, ErrNoDevice
		},
	}}
	got := Share(c).(MetadataProvider).Describe(t.Context(), devices)
	want := map[string]Metadata{
		"0-1.1.1": {Serial: "4336bd41", Product: "msm8916", Model: "msm8916_32_512", Device: "msm8916"},
		"0-2":     {Serial: "4336bd41", Model: "Phone"},
		"0-3":     {Serial: "locked"},
		"1-4":     {Serial: "windows", Product: "win-product", Device: "win-device"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("metadata = %+v, want %+v", got, want)
	}
	if got := s.log(); !reflect.DeepEqual(got, []string{"host:devices-l"}) {
		t.Fatalf("metadata opened a device service: %q", got)
	}
	if c.Info().LastTransport != "" {
		t.Fatal("metadata lookup counted as a device connection")
	}
}

func TestMetadataMissingAndAmbiguous(t *testing.T) {
	s := newFakeServer(t, "a device usb:0-1 model:A\nb device usb:0-1 model:B\n")
	if got := (ServerConnector{Addr: s.addr()}).Describe(t.Context(), []usb.DeviceInfo{at(0, 1)}); len(got) != 0 {
		t.Fatalf("ambiguous USB location got a label: %+v", got)
	}
	if got := (ServerConnector{Addr: closedAddr(t)}).Describe(t.Context(), []usb.DeviceInfo{at(0, 1)}); len(got) != 0 {
		t.Fatalf("unavailable server got a label: %+v", got)
	}
}

func TestNativeMetadataSurvivesClosedLeaseWithoutOpeningServices(t *testing.T) {
	dev := newFakeDevice()
	a := oldADBD(dev)
	a.auth, a.t = false, t
	a.banner = "device::ro.product.name=stock;ro.product.model=msm8916_32_512;ro.product.device=msm8916;\x00"
	a.start()
	opened := 0
	native := USBConnector{Open: func(context.Context, usb.DeviceInfo, usb.Interface) (usb.Bulk, error) {
		opened++
		return a.b, nil
	}}
	c := Share(native)
	info := at(0, 1, 1, 1)
	info.Serial, info.Address = "4336bd41", 8
	want := Metadata{Serial: "4336bd41", Product: "stock", Model: "msm8916_32_512", Device: "msm8916"}
	provider := c.(MetadataProvider)
	if got := provider.Describe(t.Context(), []usb.DeviceInfo{info}); len(got) != 0 || opened != 0 {
		t.Fatalf("unknown device was opened for metadata: %+v, opens=%d", got, opened)
	}
	d, err := c.Connect(t.Context(), info)
	if err != nil {
		t.Fatal(err)
	}
	if got := d.(interface{ Metadata() Metadata }).Metadata(); got != want {
		t.Fatalf("native metadata = %+v, want %+v", got, want)
	}
	d.Close()
	if got := provider.Describe(t.Context(), []usb.DeviceInfo{info})[info.Location()]; got != want || opened != 1 {
		t.Fatalf("cached metadata = %+v, opens=%d", got, opened)
	}
	dev.mu.Lock()
	services := append([]string(nil), dev.shells...)
	dev.mu.Unlock()
	if len(services) != 0 {
		t.Fatalf("metadata opened services: %q", services)
	}
	// A new USB address at the same port must not inherit an earlier connection's banner.
	replacement := info
	replacement.Address++
	if got := provider.Describe(t.Context(), []usb.DeviceInfo{replacement}); len(got) != 0 || opened != 1 {
		t.Fatalf("stale metadata = %+v, opens=%d", got, opened)
	}
	// Once it disappeared, even a reused address has no cached label.
	if got := provider.Describe(t.Context(), []usb.DeviceInfo{info}); len(got) != 0 {
		t.Fatalf("disconnected device kept metadata: %+v", got)
	}
}

func TestMetadataNameFallbacks(t *testing.T) {
	for _, tc := range []struct {
		m    Metadata
		name string
	}{
		{Metadata{Model: "Model", Product: "product", Device: "device"}, "Model"},
		{Metadata{Model: "  ", Product: "product", Device: "device"}, "product"},
		{Metadata{Device: "device"}, "device"},
		{Metadata{}, ""},
	} {
		if got := tc.m.Name(); got != tc.name {
			t.Errorf("%+v: name = %q, want %q", tc.m, got, tc.name)
		}
	}
}
