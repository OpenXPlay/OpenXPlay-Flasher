// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-22

package adb

import (
	"context"
	"strings"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/usb"
)

// Metadata is advertised by the adb server or a transport's CNXN banner. It is for display,
// never for confirming a model or authorizing operations on a device.
type Metadata struct {
	Serial, Product, Model, Device string
}

func (m Metadata) Name() string {
	for _, name := range []string{m.Model, m.Product, m.Device} {
		if name = strings.TrimSpace(name); name != "" {
			return name
		}
	}
	return ""
}

// MetadataProvider describes already-listed USB devices by location using host inventory or
// cached connection metadata only. It must not connect to a device or open any device service.
// Missing metadata, including an unavailable server, simply leaves that location out.
type MetadataProvider interface {
	Describe(context.Context, []usb.DeviceInfo) map[string]Metadata
}

func (s ServerConnector) Describe(ctx context.Context, devices []usb.DeviceInfo) map[string]Metadata {
	out := map[string]Metadata{}
	if len(devices) == 0 {
		return out
	}
	entries, err := s.devices(ctx)
	if err != nil {
		return out
	}
	for _, dev := range devices {
		if e, err := pickEntry(entries, dev); err == nil {
			out[dev.Location()] = e.metadata
		}
	}
	return out
}

func (a *AutoConnector) Describe(ctx context.Context, devices []usb.DeviceInfo) map[string]Metadata {
	return a.Server.Describe(ctx, devices)
}

func bannerProperties(banner []byte) map[string]string {
	_, props, _ := strings.Cut(strings.TrimRight(string(banner), "\x00"), "::")
	out := map[string]string{}
	for _, p := range strings.Split(props, ";") {
		if key, value, ok := strings.Cut(p, "="); ok {
			out[key] = value
		}
	}
	return out
}

func (d *device) Metadata() Metadata {
	m := d.metadata
	if d.serial != "" {
		m.Serial = d.serial
	}
	return m
}

func transportMetadata(d Device, fallback string) Metadata {
	var m Metadata
	if source, ok := d.(interface{ Metadata() Metadata }); ok {
		m = source.Metadata()
	}
	if m.Serial == "" {
		m.Serial = TransportSerial(d, fallback)
	}
	return m
}
