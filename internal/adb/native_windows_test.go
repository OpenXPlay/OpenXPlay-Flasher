// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only

package adb

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/usb"
)

type descriptorSerialBulk struct {
	*fakeBulk
	serial string
	err    error
}

func (b *descriptorSerialBulk) SerialNumber() (string, error) { return b.serial, b.err }

// Windows uppercases USB instance IDs. A successfully read USB string descriptor
// is authoritative; otherwise keep the existing transport serial as a fallback.
func TestNativeWindowsDescriptorSerial(t *testing.T) {
	for _, tc := range []struct {
		name, serial, want string
		err                error
	}{
		{name: "preserve descriptor case", serial: "f9816ac0", want: "f9816ac0"},
		{name: "missing descriptor", want: "F9816AC0"},
		{name: "descriptor failure", serial: "ignored", err: errors.New("descriptor unavailable"), want: "F9816AC0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &descriptorSerialBulk{fakeBulk: newFakeBulk(512), serial: tc.serial, err: tc.err}
			t.Cleanup(func() { b.Close() })
			a := &fakeADBD{t: t, b: b.fakeBulk, dev: newFakeDevice(), version: versionSkipChecksum,
				maxData: hostMaxData, banner: "device::ro.serialno=banner;features=shell_v2"}
			a.start()
			c := USBConnector{Open: func(context.Context, usb.DeviceInfo, usb.Interface) (usb.Bulk, error) { return b, nil }}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			dev := at(0, 1)
			dev.Serial = "F9816AC0"
			d, err := c.Connect(ctx, dev)
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			if got := TransportSerial(d, ""); got != tc.want {
				t.Errorf("transport serial = %q, want %q", got, tc.want)
			}
			if got := d.(interface{ Metadata() Metadata }).Metadata().Serial; got != tc.want {
				t.Errorf("metadata serial = %q, want %q", got, tc.want)
			}
		})
	}
}

// Opt in with an exact physical location. Ordinary tests never connect to real
// hardware. This performs only identity reads through native WinUSB, bypassing
// any adb server, and exercises transport cancellation when Close stops the reader.
func TestWindowsNativeADBReadOnly(t *testing.T) {
	location := os.Getenv("OPENXPLAY_USB_ADB_TEST_LOCATION")
	if location == "" {
		t.Skip("set OPENXPLAY_USB_ADB_TEST_LOCATION to an exact USB location for a read-only hardware test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	bus := usb.NewHostBus()
	defer bus.Close()
	devices, err := bus.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var target *usb.DeviceInfo
	for i := range devices {
		if devices[i].Location() == location && devices[i].HasInterface(adbInterface) {
			if target != nil {
				t.Fatalf("multiple ADB devices at %s", location)
			}
			target = &devices[i]
		}
	}
	if target == nil {
		t.Fatalf("no ADB device at exact location %s", location)
	}
	c := USBConnector{KeyPath: os.Getenv("OPENXPLAY_USB_ADB_TEST_KEY")}
	d, err := c.Connect(ctx, *target)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	// Stock Android 4.4 has getprop but may not include uname. Use only the
	// platform's identity reader so missing optional tools do not fail USB QA.
	result, err := d.Shell(ctx, "if command -v getprop >/dev/null 2>&1; then getprop ro.serialno; getprop ro.product.model; else uname -s; fi")
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("read-only identity shell = %+v, %v", result, err)
	}
	t.Logf("native ADB %s at %s serial %q: %s", target.ID(), location, TransportSerial(d, ""), strings.TrimSpace(string(result.Stdout)))
	closed := make(chan error, 1)
	go func() { closed <- d.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("native ADB Close did not cancel its background USB read")
	}
}
