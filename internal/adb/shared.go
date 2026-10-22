// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package adb

import (
	"context"
	"fmt"
	"io"
	"maps"
	"sync"
	"sync/atomic"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/usb"
)

// OpenTCP opens a device TCP service on either transport. It does not use shell commands,
// a listening device interface, or an external adb executable.
func (d *device) OpenTCP(ctx context.Context, port int) (io.ReadWriteCloser, error) {
	if port < 1 || port > 65535 {
		return nil, fmt.Errorf("adb: invalid TCP port")
	}
	return d.open(ctx, fmt.Sprintf("tcp:%d", port))
}

type TCPDevice interface {
	OpenTCP(context.Context, int) (io.ReadWriteCloser, error)
}

func SupportsTCP(d Device) bool {
	if lease, ok := d.(*deviceLease); ok {
		return SupportsTCP(lease.Device)
	}
	_, ok := d.(TCPDevice)
	return ok
}

// SharedConnector lets a long-lived tunnel and short-lived shell operations multiplex streams
// on the same USB transport. The last lease closes the transport; USB address changes make
// a new entry. It preserves the underlying connector's metadata and serial locator.
type SharedConnector struct {
	Connector Connector
	mu        sync.Mutex
	entries   map[string]*sharedEntry
	known     map[string]Metadata
}
type sharedEntry struct {
	Device
	refs int
}
type deviceLease struct {
	Device
	owner  *SharedConnector
	entry  *sharedEntry
	key    string
	closed atomic.Bool
}

func Share(c Connector) Connector {
	if c == nil {
		return nil
	}
	return &SharedConnector{Connector: c, entries: map[string]*sharedEntry{}, known: map[string]Metadata{}}
}

func usbKey(dev usb.DeviceInfo) string {
	return fmt.Sprintf("%s/%s/%d/%s", dev.ID(), dev.Location(), dev.Address, dev.Serial)
}

func (c *SharedConnector) Connect(ctx context.Context, dev usb.DeviceInfo) (Device, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	key := usbKey(dev)
	e := c.entries[key]
	if e == nil {
		d, err := c.Connector.Connect(ctx, dev)
		if err != nil {
			return nil, err
		}
		e = &sharedEntry{Device: d}
		c.entries[key] = e
		c.known[key] = transportMetadata(d, dev.Serial)
	}
	e.refs++
	return &deviceLease{Device: e.Device, owner: c, entry: e, key: key}, nil
}
func (d *deviceLease) Close() error {
	if d.closed.Swap(true) {
		return nil
	}
	d.owner.mu.Lock()
	defer d.owner.mu.Unlock()
	d.entry.refs--
	if d.entry.refs == 0 {
		delete(d.owner.entries, d.key)
		return d.entry.Device.Close()
	}
	return nil
}
func (d *deviceLease) OpenTCP(ctx context.Context, port int) (io.ReadWriteCloser, error) {
	if d.closed.Load() {
		return nil, errClosed
	}
	if tcp, ok := d.Device.(TCPDevice); ok {
		return tcp.OpenTCP(ctx, port)
	}
	return nil, fmt.Errorf("adb: TCP streams are unavailable")
}
func (d *deviceLease) Serial() string     { return TransportSerial(d.Device, "") }
func (d *deviceLease) Metadata() Metadata { return transportMetadata(d.Device, "") }
func (d *deviceLease) RebootBootloader(ctx context.Context) error {
	if d.closed.Load() {
		return errClosed
	}
	return RebootBootloader(ctx, d.Device)
}
func (c *SharedConnector) Info() ConnectionInfo {
	if info, ok := c.Connector.(interface{ Info() ConnectionInfo }); ok {
		return info.Info()
	}
	return ConnectionInfo{}
}
func (c *SharedConnector) LocateSerial(ctx context.Context, serial string, devices []usb.DeviceInfo) (usb.DeviceInfo, error) {
	if locator, ok := c.Connector.(interface {
		LocateSerial(context.Context, string, []usb.DeviceInfo) (usb.DeviceInfo, error)
	}); ok {
		return locator.LocateSerial(ctx, serial, devices)
	}
	return usb.DeviceInfo{}, ErrNoDevice
}

// Describe retains CNXN metadata after the last lease closes, until the USB device disappears
// or its address changes. Looking up a label never creates another lease or opens a service.
func (c *SharedConnector) Describe(ctx context.Context, devices []usb.DeviceInfo) map[string]Metadata {
	out := map[string]Metadata{}
	if source, ok := c.Connector.(MetadataProvider); ok {
		out = maps.Clone(source.Describe(ctx, devices))
	}
	if out == nil {
		out = map[string]Metadata{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	present := map[string]bool{}
	for _, dev := range devices {
		key := usbKey(dev)
		present[key] = true
		if m, ok := c.known[key]; ok {
			if _, listed := out[dev.Location()]; !listed {
				out[dev.Location()] = m
			}
		}
	}
	for key := range c.known {
		if !present[key] {
			delete(c.known, key)
		}
	}
	return out
}
