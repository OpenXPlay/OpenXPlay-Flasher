// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package adb

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"testing"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/usb"
)

func closedAddr(t *testing.T) string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

func TestAutoConnector(t *testing.T) {
	s, _, _ := newServerWithDongles(t)
	dev := newFakeDevice()
	dev.commands["id"] = fakeCmd{stdout: "native\n"}
	opened := 0
	native := func(open error) USBConnector {
		return USBConnector{KeyPath: filepath.Join(t.TempDir(), "adbkey"),
			Open: func(context.Context, usb.DeviceInfo, usb.Interface) (usb.Bulk, error) {
				opened++
				if open != nil {
					return nil, open
				}
				a := oldADBD(dev)
				a.auth, a.t = false, t
				a.start()
				return a.b, nil
			}}
	}
	ctx := context.Background()
	shell := func(c Connector, dev usb.DeviceInfo) (string, error) {
		d, err := c.Connect(ctx, dev)
		if err != nil {
			return "", err
		}
		defer d.Close()
		r, err := d.Shell(ctx, "id")
		return string(r.Stdout), err
	}

	// The server lists the device: it is used and USB is left alone.
	if out, err := shell(&AutoConnector{Server: ServerConnector{Addr: s.addr()}, USB: native(nil)}, at(0, 1, 1, 1)); err != nil || out != "uid=0(root)\n" || opened != 0 {
		t.Fatalf("listed: %q %v (opened %d)", out, err, opened)
	}
	// The server does not list it, or no server runs: native USB.
	if out, err := shell(&AutoConnector{Server: ServerConnector{Addr: s.addr()}, USB: native(nil)}, at(0, 7)); err != nil || out != "native\n" {
		t.Fatalf("unlisted: %q %v", out, err)
	}
	if out, err := shell(&AutoConnector{Server: ServerConnector{Addr: closedAddr(t)}, USB: native(nil)}, at(0, 7)); err != nil || out != "native\n" {
		t.Fatalf("no server: %q %v", out, err)
	}
	// A listed device that is not ready is reported, not opened over USB.
	opened = 0
	if _, err := shell(&AutoConnector{Server: ServerConnector{Addr: s.addr()}, USB: native(nil)}, at(0, 1, 3)); err == nil || opened != 0 {
		t.Fatalf("unauthorized: %v (opened %d)", err, opened)
	}
	// Builds without USB transfers.
	unsupported := native(usb.ErrTransfersUnsupported)
	if _, err := shell(&AutoConnector{Server: ServerConnector{Addr: s.addr()}, USB: unsupported}, at(0, 7)); !errors.Is(err, ErrNoDevice) {
		t.Fatalf("unlisted, no USB: %v", err)
	}
	if _, err := shell(&AutoConnector{Server: ServerConnector{Addr: closedAddr(t)}, USB: unsupported}, at(0, 7)); !errors.Is(err, usb.ErrTransfersUnsupported) {
		t.Fatalf("no server, no USB: %v", err)
	}
	if _, err := shell(&AutoConnector{Server: ServerConnector{Addr: closedAddr(t)}, USB: native(usb.ErrBusy)}, at(0, 7)); !errors.Is(err, ErrBusy) {
		t.Fatalf("busy: %v", err)
	}
	if _, ok := NewConnector().(*AutoConnector); !ok {
		t.Fatal("NewConnector is not the auto connector")
	}
}
