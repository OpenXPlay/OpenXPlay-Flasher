// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package adb

import (
	"context"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/usb"
)

func TestTCPStreamsOnBothTransports(t *testing.T) {
	for _, mode := range []string{"server", "native"} {
		t.Run(mode, func(t *testing.T) {
			dev := newFakeDevice()
			var d Device
			if mode == "server" {
				s := newFakeServer(t, "fixture device usb:0-1 transport_id:1\n")
				s.byID["1"] = dev
				var err error
				d, err = (ServerConnector{Addr: s.addr()}).Connect(t.Context(), at(0, 1))
				if err != nil {
					t.Fatal(err)
				}
			} else {
				d = nativeDevice(t, oldADBD(dev), filepath.Join(t.TempDir(), "adbkey"))
			}
			defer d.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			stream, err := d.(TCPDevice).OpenTCP(ctx, 6975)
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()
			if _, err = stream.Write([]byte("fixture")); err != nil {
				t.Fatal(err)
			}
			buf := make([]byte, 7)
			if _, err = io.ReadFull(stream, buf); err != nil || string(buf) != "fixture" {
				t.Fatalf("TCP echo: %q %v", buf, err)
			}
			if _, err = d.(TCPDevice).OpenTCP(ctx, 0); err == nil {
				t.Fatal("invalid port accepted")
			}
		})
	}
}

type countedConnector struct {
	d     Device
	opens int
}

type rebootableDevice struct {
	Device
	reboots int
}

func (d *rebootableDevice) RebootBootloader(context.Context) error { d.reboots++; return nil }
func (d *rebootableDevice) OpenTCP(ctx context.Context, port int) (io.ReadWriteCloser, error) {
	return d.Device.(TCPDevice).OpenTCP(ctx, port)
}

func (c *countedConnector) Connect(context.Context, usb.DeviceInfo) (Device, error) {
	c.opens++
	return c.d, nil
}
func TestSharedTransportLeases(t *testing.T) {
	dev := newFakeDevice()
	d := nativeDevice(t, oldADBD(dev), filepath.Join(t.TempDir(), "adbkey"))
	rebootable := &rebootableDevice{Device: d}
	c := &countedConnector{d: rebootable}
	shared := Share(c)
	usbDev := at(0, 1)
	first, err := shared.Connect(t.Context(), usbDev)
	if err != nil {
		t.Fatal(err)
	}
	second, err := shared.Connect(t.Context(), usbDev)
	if err != nil {
		t.Fatal(err)
	}
	if c.opens != 1 {
		t.Fatal("second lease opened another USB transport")
	}
	if err := RebootBootloader(t.Context(), second); err != nil || rebootable.reboots != 1 {
		t.Fatal("lease hid the native bootloader reboot service")
	}
	first.Close()
	first.Close()
	stream, err := second.(TCPDevice).OpenTCP(t.Context(), 6975)
	if err != nil {
		t.Fatal(err)
	}
	stream.Close()
	second.Close()
	if _, err = second.(TCPDevice).OpenTCP(t.Context(), 6975); err == nil {
		t.Fatal("closed lease reopened a stream")
	}
}
