// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package adb

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/usb"
)

// DefaultServerAddr is where the adb server listens.
const DefaultServerAddr = "127.0.0.1:5037"

// errNoServer: nothing answers at the server address.
var errNoServer = errors.New("adb: no adb server is running")

// ServerConnector reaches devices through a running adb server, over its host protocol: requests
// are a 4-digit hex length and the text, answered by OKAY or by FAIL with a hex-length message.
type ServerConnector struct {
	// Addr is the server's address; empty means DefaultServerAddr.
	Addr string
}

// Connect finds the device by its USB port in host:devices-l. Several OpenStick dongles share one
// serial number, so a device is picked by its usb: path and reached by its transport id; the
// serial is used only when the server lists no usb paths (Windows) and the serial is unique.
func (s ServerConnector) Connect(ctx context.Context, dev usb.DeviceInfo) (Device, error) {
	entries, err := s.devices(ctx)
	if err != nil {
		return nil, err
	}
	e, err := pickEntry(entries, dev)
	if err != nil {
		return nil, err
	}
	if e.state != "device" {
		return nil, fmt.Errorf("adb: the device at %s is %s", dev.Location(), e.state)
	}
	t := &serverTransport{conn: s}
	features := "host-serial:" + e.serial + ":features"
	switch {
	case e.transportID != "":
		t.selector = "host:transport-id:" + e.transportID
		features = "host-transport-id:" + e.transportID + ":features"
	case countSerial(entries, e.serial) == 1:
		t.selector = "host:transport:" + e.serial
	default:
		return nil, fmt.Errorf("adb: the adb server is too old to tell apart the devices with serial %s", e.serial)
	}
	// A server without the features request is old, and so is its device: no shell v2.
	list, err := s.query(ctx, features)
	if err != nil && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	d := newDevice(t, splitList(list))
	d.serial = e.serial
	d.metadata = e.metadata
	return d, nil
}

type serverEntry struct {
	serial, state, usb, transportID string
	metadata                        Metadata
}

// devices lists the server's devices.
func (s ServerConnector) devices(ctx context.Context) ([]serverEntry, error) {
	list, err := s.query(ctx, "host:devices-l")
	if err != nil {
		return nil, err
	}
	return parseDevicesL(list), nil
}

// parseDevicesL reads host:devices-l lines: "<serial> <state> [usb:<path>] [product:...]
// [model:...] [device:...] [transport_id:<n>]". The state may hold spaces ("no permissions (...)").
func parseDevicesL(list string) []serverEntry {
	var out []serverEntry
	for _, line := range strings.Split(list, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		e := serverEntry{serial: f[0], metadata: Metadata{Serial: f[0]}}
		var state []string
		inState := true
		for _, tok := range f[1:] {
			key, value, ok := strings.Cut(tok, ":")
			switch {
			case ok && key == "usb":
				e.usb = value
			case ok && key == "transport_id":
				e.transportID = value
			case ok && key == "product":
				e.metadata.Product = value
			case ok && key == "model":
				e.metadata.Model = value
			case ok && key == "device":
				e.metadata.Device = value
			case ok && key == "features":
			default:
				if inState {
					state = append(state, tok)
				}
				continue
			}
			inState = false
		}
		e.state = strings.Join(state, " ")
		out = append(out, e)
	}
	return out
}

func pickEntry(entries []serverEntry, dev usb.DeviceInfo) (serverEntry, error) {
	var match []serverEntry
	for _, e := range entries {
		if e.usb != "" && e.usb == dev.Location() {
			match = append(match, e)
		}
	}
	if len(match) == 0 && dev.Serial != "" {
		// A duplicate with a known path is still ambiguous: it must not make
		// a pathless entry look like the only device with this serial.
		if e, unique := uniqueInventorySerial(entries, dev); unique && e.usb == "" {
			match = append(match, e)
		}
	}
	switch len(match) {
	case 0:
		return serverEntry{}, fmt.Errorf("%w (the adb server does not list %s)", ErrNoDevice, dev.Location())
	case 1:
		return match[0], nil
	}
	return serverEntry{}, fmt.Errorf("adb: the adb server lists %d devices at %s", len(match), dev.Location())
}

// Inventory matching accounts for Windows PnP serial normalization. Protocol
// selectors and the serial returned by the connected device remain case sensitive.
func uniqueInventorySerial(entries []serverEntry, dev usb.DeviceInfo) (serverEntry, bool) {
	if dev.Serial == "" {
		return serverEntry{}, false
	}
	var match serverEntry
	found := false
	for _, e := range entries {
		if !dev.MatchesSerial(e.serial) {
			continue
		}
		if found {
			return serverEntry{}, false
		}
		match, found = e, true
	}
	return match, found
}

func countSerial(entries []serverEntry, serial string) int {
	n := 0
	for _, e := range entries {
		if e.serial == serial {
			n++
		}
	}
	return n
}

func splitList(s string) []string {
	var out []string
	for _, f := range strings.Split(strings.TrimSpace(s), ",") {
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

// serverConn is one connection to the server with its I/O bound to a context: the deadline is
// moved into the past when the context ends.
type serverConn struct {
	net.Conn
	stop func() bool
}

func (s ServerConnector) dial(ctx context.Context) (*serverConn, error) {
	addr := s.Addr
	if addr == "" {
		addr = DefaultServerAddr
	}
	d := net.Dialer{Timeout: 3 * time.Second}
	c, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w at %s (%v)", errNoServer, addr, err)
	}
	// Only once the context is done, so a failed read or write always finds ctx.Err() set.
	stop := context.AfterFunc(ctx, func() { c.SetDeadline(time.Unix(1, 0)) })
	return &serverConn{Conn: c, stop: stop}, nil
}

func (c *serverConn) Close() error {
	c.stop()
	return c.Conn.Close()
}

// request sends one host request and reads its status.
func (c *serverConn) request(req string) error {
	if len(req) > 0xffff {
		return fmt.Errorf("adb: request of %d bytes is too long", len(req))
	}
	if _, err := io.WriteString(c, fmt.Sprintf("%04x%s", len(req), req)); err != nil {
		return err
	}
	var status [4]byte
	if _, err := io.ReadFull(c, status[:]); err != nil {
		return err
	}
	switch string(status[:]) {
	case "OKAY":
		return nil
	case "FAIL":
		msg, err := c.readString()
		if err != nil {
			return err
		}
		return &ServerError{Request: req, Message: msg}
	}
	return fmt.Errorf("adb: unexpected server reply %q", status[:])
}

// readString reads a hex-length string.
func (c *serverConn) readString() (string, error) {
	var hex [4]byte
	if _, err := io.ReadFull(c, hex[:]); err != nil {
		return "", err
	}
	n, err := strconv.ParseUint(string(hex[:]), 16, 16)
	if err != nil {
		return "", fmt.Errorf("adb: bad server length %q", hex[:])
	}
	b := make([]byte, n)
	_, err = io.ReadFull(c, b)
	return string(b), err
}

// query runs a host request that answers with a string.
func (s ServerConnector) query(ctx context.Context, req string) (string, error) {
	c, err := s.dial(ctx)
	if err != nil {
		return "", err
	}
	defer c.Close()
	if err := c.request(req); err != nil {
		return "", callErr(ctx, err)
	}
	str, err := c.readString()
	return str, callErr(ctx, err)
}

// ServerError is a FAIL reply of the adb server.
type ServerError struct{ Request, Message string }

func (e *ServerError) Error() string { return "adb server: " + e.Message }

// serverTransport opens each service on a new server connection switched to the device.
type serverTransport struct {
	conn     ServerConnector
	selector string // host:transport-id:<id>, or host:transport:<serial>
}

func (t *serverTransport) open(ctx context.Context, name string) (service, error) {
	c, err := t.conn.dial(ctx)
	if err != nil {
		return nil, err
	}
	if err := c.request(t.selector); err != nil {
		c.Close()
		var se *ServerError
		if errors.As(err, &se) {
			return nil, fmt.Errorf("%w (%v)", ErrNoDevice, err)
		}
		return nil, err
	}
	if err := c.request(name); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

func (t *serverTransport) close() error { return nil }

// LocateSerial resolves an explicitly designated serial using the host inventory only. No
// device service is opened, and duplicate serials cannot select a device on the user's behalf.
func (s ServerConnector) LocateSerial(ctx context.Context, serial string, devices []usb.DeviceInfo) (usb.DeviceInfo, error) {
	entries, err := s.devices(ctx)
	if err != nil {
		return usb.DeviceInfo{}, err
	}
	e, unique := uniqueInventorySerial(entries, usb.DeviceInfo{Serial: serial})
	if !unique {
		return usb.DeviceInfo{}, ErrNoDevice
	}
	var match usb.DeviceInfo
	found := false
	for _, dev := range devices {
		if e.usb != "" && e.usb == dev.Location() || e.usb == "" && dev.MatchesSerial(e.serial) {
			if found {
				return usb.DeviceInfo{}, ErrNoDevice
			}
			match, found = dev, true
		}
	}
	if found {
		return match, nil
	}
	return usb.DeviceInfo{}, ErrNoDevice
}
func (a *AutoConnector) LocateSerial(ctx context.Context, serial string, devices []usb.DeviceInfo) (usb.DeviceInfo, error) {
	return a.Server.LocateSerial(ctx, serial, devices)
}
