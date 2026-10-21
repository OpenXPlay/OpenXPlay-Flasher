// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package adb

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/usb"
)

// fakeServer speaks the adb server's host protocol for devices it holds by transport id.
type fakeServer struct {
	ln       net.Listener
	devicesL string
	byID     map[string]*fakeDevice // transport id -> device
	bySerial map[string]*fakeDevice
	features map[string]string // transport id or serial -> features
	// noFeatures answers the features request with FAIL, as an old server.
	noFeatures bool

	mu       sync.Mutex
	requests []string
}

func newFakeServer(t *testing.T, devicesL string) *fakeServer {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &fakeServer{ln: ln, devicesL: devicesL, byID: map[string]*fakeDevice{},
		bySerial: map[string]*fakeDevice{}, features: map[string]string{}}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.handle(c)
		}
	}()
	return s
}

func (s *fakeServer) addr() string { return s.ln.Addr().String() }

func (s *fakeServer) log() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.requests...)
}

func (s *fakeServer) handle(c net.Conn) {
	defer c.Close()
	okay := func() { io.WriteString(c, "OKAY") }
	str := func(v string) { fmt.Fprintf(c, "%04x%s", len(v), v) }
	fail := func(msg string) { io.WriteString(c, "FAIL"); str(msg) }
	var dev *fakeDevice
	for {
		var hex [4]byte
		if _, err := io.ReadFull(c, hex[:]); err != nil {
			return
		}
		n, _ := strconv.ParseUint(string(hex[:]), 16, 16)
		b := make([]byte, n)
		if _, err := io.ReadFull(c, b); err != nil {
			return
		}
		req := string(b)
		s.mu.Lock()
		s.requests = append(s.requests, req)
		s.mu.Unlock()
		if dev != nil {
			okay()
			dev.serve(c, req)
			return
		}
		switch {
		case req == "host:devices-l":
			okay()
			str(s.devicesL)
			return
		case strings.HasSuffix(req, ":features"):
			key := strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(req, "host-transport-id:"), "host-serial:"), ":features")
			if s.noFeatures {
				fail("unknown host service")
				return
			}
			okay()
			str(s.features[key])
			return
		case strings.HasPrefix(req, "host:transport-id:"):
			dev = s.byID[strings.TrimPrefix(req, "host:transport-id:")]
		case strings.HasPrefix(req, "host:transport:"):
			dev = s.bySerial[strings.TrimPrefix(req, "host:transport:")]
		default:
			fail("unknown host service")
			return
		}
		if dev == nil {
			fail("device not found")
			return
		}
		okay()
	}
}

// Two OpenStick dongles with the same serial, a phone, an unauthorized device and a device the
// server may not open, in the format of adb devices -l.
const devicesL = `0123456789             device usb:0-1.1.1 product:occam model:Nexus_4 device:mako transport_id:16
0123456789             device usb:0-1.2 product:occam model:Nexus_4 device:mako transport_id:17
7DJF6XLJMN6DCEKR       device usb:0-1.1.4 product:pond model:2409BRN2CC device:pond transport_id:12
R5CN30JQHZF            unauthorized usb:0-1.3 transport_id:14
????????????           no permissions (missing udev rules? user is in the plugdev group); see [http://developer.android.com/tools/device.html] usb:1-4 transport_id:3
192.168.1.20:5555      offline product:x model:y device:z transport_id:9
`

func at(bus int, path ...int) usb.DeviceInfo {
	return usb.DeviceInfo{VendorID: 0x18d1, ProductID: 0xd001, Bus: bus, Path: path}
}

func TestParseDevicesL(t *testing.T) {
	got := parseDevicesL(devicesL)
	want := []serverEntry{
		{"0123456789", "device", "0-1.1.1", "16"},
		{"0123456789", "device", "0-1.2", "17"},
		{"7DJF6XLJMN6DCEKR", "device", "0-1.1.4", "12"},
		{"R5CN30JQHZF", "unauthorized", "0-1.3", "14"},
		{"????????????", "no permissions (missing udev rules? user is in the plugdev group); see [http://developer.android.com/tools/device.html]", "1-4", "3"},
		{"192.168.1.20:5555", "offline", "", "9"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseDevicesL:\n got %q\nwant %q", got, want)
	}
}

func TestPickEntry(t *testing.T) {
	entries := parseDevicesL(devicesL)
	if e, err := pickEntry(entries, at(0, 1, 2)); err != nil || e.transportID != "17" {
		t.Fatalf("0-1.2: %+v %v", e, err)
	}
	// The serial is shared, so it never stands in for the path.
	dev := at(0, 9)
	dev.Serial = "0123456789"
	if _, err := pickEntry(entries, dev); !errors.Is(err, ErrNoDevice) {
		t.Fatalf("shared serial: %v", err)
	}
	// A server without usb paths (Windows) is matched by a unique serial.
	windows := parseDevicesL("0123456789 device product:occam model:Nexus_4 device:mako transport_id:3\nABC device transport_id:4\n")
	if e, err := pickEntry(windows, dev); err != nil || e.transportID != "3" {
		t.Fatalf("serial fallback: %+v %v", e, err)
	}
	if _, err := pickEntry(windows, at(0, 9)); !errors.Is(err, ErrNoDevice) {
		t.Fatalf("no serial: %v", err)
	}
	twice := parseDevicesL("0123456789 device transport_id:3\n0123456789 device transport_id:4\n")
	if _, err := pickEntry(twice, dev); !errors.Is(err, ErrNoDevice) {
		t.Fatalf("duplicate serial: %v", err)
	}
}

func newServerWithDongles(t *testing.T) (*fakeServer, *fakeDevice, *fakeDevice) {
	s := newFakeServer(t, devicesL)
	a, b := newFakeDevice(), newFakeDevice()
	a.commands["id"] = fakeCmd{stdout: "uid=0(root)\n"}
	b.commands["id"] = fakeCmd{stdout: "uid=1(other)\n"}
	s.byID["16"], s.byID["17"] = a, b
	return s, a, b
}

func TestServerSelectsByUSBPath(t *testing.T) {
	s, _, _ := newServerWithDongles(t)
	ctx := context.Background()
	for _, tc := range []struct {
		dev  usb.DeviceInfo
		want string
	}{{at(0, 1, 1, 1), "uid=0(root)\n"}, {at(0, 1, 2), "uid=1(other)\n"}} {
		d, err := ServerConnector{Addr: s.addr()}.Connect(ctx, tc.dev)
		if err != nil {
			t.Fatal(err)
		}
		r, err := d.Shell(ctx, "id")
		if err != nil || string(r.Stdout) != tc.want || r.ExitCode != 0 {
			t.Fatalf("%s: %q %v", tc.dev.Location(), r.Stdout, err)
		}
		d.Close()
	}
	log := s.log()
	if !contains(log, "host:transport-id:16") || !contains(log, "host:transport-id:17") || !contains(log, "host-transport-id:16:features") {
		t.Fatalf("requests %q", log)
	}
	if _, err := (ServerConnector{Addr: s.addr()}).Connect(ctx, at(0, 5)); !errors.Is(err, ErrNoDevice) {
		t.Fatalf("unlisted: %v", err)
	}
	if _, err := (ServerConnector{Addr: s.addr()}).Connect(ctx, at(0, 1, 3)); err == nil || !strings.Contains(err.Error(), "unauthorized") {
		t.Fatalf("unauthorized: %v", err)
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func TestServerShellV2(t *testing.T) {
	s, dev, _ := newServerWithDongles(t)
	s.features["16"] = "cmd,shell_v2,stat_v2"
	dev.commands["echo out; echo err >&2; exit 3"] = fakeCmd{stdout: "out\n", stderr: "err\n", code: 3}
	ctx := context.Background()
	d, err := ServerConnector{Addr: s.addr()}.Connect(ctx, at(0, 1, 1, 1))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	r, err := d.Shell(ctx, "echo out; echo err >&2; exit 3")
	if err != nil || string(r.Stdout) != "out\n" || string(r.Stderr) != "err\n" || r.ExitCode != 3 {
		t.Fatalf("%+v %v", r, err)
	}
	if !contains(s.log(), "shell,v2,raw:echo out; echo err >&2; exit 3") {
		t.Fatalf("requests %q", s.log())
	}
}

func TestServerShellSentinel(t *testing.T) {
	for _, pty := range []bool{false, true} {
		s, dev, _ := newServerWithDongles(t)
		s.noFeatures = true
		dev.pty = pty
		dev.commands["echo out; echo err >&2; exit 3"] = fakeCmd{stdout: "out\n", stderr: "err\n", code: 3}
		dev.commands["printf x"] = fakeCmd{stdout: "x"}
		ctx := context.Background()
		d, err := ServerConnector{Addr: s.addr()}.Connect(ctx, at(0, 1, 1, 1))
		if err != nil {
			t.Fatal(err)
		}
		nl := "\n"
		if pty {
			nl = "\r\n"
		}
		r, err := d.Shell(ctx, "echo out; echo err >&2; exit 3")
		if err != nil || string(r.Stdout) != "out"+nl+"err"+nl || len(r.Stderr) != 0 || r.ExitCode != 3 {
			t.Fatalf("pty %v: %+v %v", pty, r, err)
		}
		if r, err = d.Shell(ctx, "printf x"); err != nil || string(r.Stdout) != "x" || r.ExitCode != 0 {
			t.Fatalf("pty %v: %+v %v", pty, r, err)
		}
		if r, err = d.Shell(ctx, "missing"); err != nil || r.ExitCode != 127 {
			t.Fatalf("pty %v: %+v %v", pty, r, err)
		}
		d.Close()
	}
}

func TestServerSync(t *testing.T) {
	s, dev, _ := newServerWithDongles(t)
	big := bytes.Repeat([]byte("0123456789abcdef"), 10000) // 160000 bytes: three DATA chunks
	dev.files["/etc/openxplay/host.json"] = &fakeFile{data: []byte(`{"model":"UFI003"}`), perm: 0o644}
	dev.files["/big"] = &fakeFile{data: big, perm: 0o644}
	dev.files["/proc/cmdline"] = &fakeFile{data: bytes.Repeat([]byte("x"), 300), perm: 0o444, sizeZero: true}
	dev.failSend["/ro/file"] = "Read-only file system"
	ctx := context.Background()
	d, err := ServerConnector{Addr: s.addr()}.Connect(ctx, at(0, 1, 1, 1))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	testSync(t, d, dev, big)
}

// testSync runs the sync checks against a device backed by dev.
func testSync(t *testing.T, d Device, dev *fakeDevice, big []byte) {
	t.Helper()
	ctx := context.Background()
	if data, err := d.Pull(ctx, "/etc/openxplay/host.json", 1<<10); err != nil || string(data) != `{"model":"UFI003"}` {
		t.Fatalf("pull: %q %v", data, err)
	}
	if data, err := d.Pull(ctx, "/big", 1<<20); err != nil || !bytes.Equal(data, big) {
		t.Fatalf("pull big: %d bytes %v", len(data), err)
	}
	if _, err := d.Pull(ctx, "/big", 1000); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("pull over max (STAT): %v", err)
	}
	// STAT says 0 bytes: the limit holds while reading.
	if _, err := d.Pull(ctx, "/proc/cmdline", 100); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("pull over max (reading): %v", err)
	}
	if data, err := d.Pull(ctx, "/proc/cmdline", 300); err != nil || len(data) != 300 {
		t.Fatalf("pull at max: %d %v", len(data), err)
	}
	if _, err := d.Pull(ctx, "/missing", 10); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("pull missing: %v", err)
	}
	if _, err := d.Pull(ctx, "/etc", 10); err == nil {
		t.Fatal("pull of a directory succeeded")
	}
	if err := d.Push(ctx, "/tmp/a", big, 0o640); err != nil {
		t.Fatal(err)
	}
	if f := dev.file("/tmp/a"); f == nil || !bytes.Equal(f.data, big) || f.perm != 0o640 {
		t.Fatalf("pushed %+v", f)
	}
	if err := d.Push(ctx, "/tmp/empty", nil, 0o600); err != nil || dev.file("/tmp/empty") == nil {
		t.Fatalf("push empty: %v", err)
	}
	var se *SyncError
	if err := d.Push(ctx, "/ro/file", []byte("x"), 0o644); !errors.As(err, &se) || se.Message != "Read-only file system" {
		t.Fatalf("push FAIL: %v", err)
	}
	// An adbd that ignores the mode gets a chmod.
	dev.set(func() { dev.ignoreMode = true })
	if err := d.Push(ctx, "/tmp/it's", []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if f := dev.file("/tmp/it's"); f == nil || f.perm != 0o600 {
		t.Fatalf("mode not corrected: %+v", f)
	}
}

func TestServerCancel(t *testing.T) {
	s, dev, _ := newServerWithDongles(t)
	dev.commands["sleep 60"] = fakeCmd{block: true}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	d, err := ServerConnector{Addr: s.addr()}.Connect(context.Background(), at(0, 1, 1, 1))
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if _, err := d.Shell(ctx, "sleep 60"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shell: %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("cancel took %v", time.Since(start))
	}
	ctx2, cancel2 := context.WithCancel(context.Background())
	go func() { time.Sleep(50 * time.Millisecond); cancel2() }()
	if _, err := d.Shell(ctx2, "sleep 60"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Shell after cancel: %v", err)
	}
	if r, err := d.Shell(context.Background(), "id"); err != nil || r.ExitCode != 0 {
		t.Fatalf("next call: %v", err)
	}
	d.Close()
	if _, err := d.Shell(context.Background(), "id"); !errors.Is(err, errClosed) {
		t.Fatalf("after Close: %v", err)
	}
}

func TestServerGone(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	if _, err := (ServerConnector{Addr: addr}).Connect(context.Background(), at(0, 1)); !errors.Is(err, errNoServer) {
		t.Fatalf("Connect: %v", err)
	}
}

func TestServerTransportBySerial(t *testing.T) {
	// An old server without transport ids: a unique serial selects the transport.
	s := newFakeServer(t, "ABC device usb:0-2 product:p\n")
	dev := newFakeDevice()
	dev.commands["id"] = fakeCmd{stdout: "ok\n"}
	s.bySerial["ABC"] = dev
	d, err := ServerConnector{Addr: s.addr()}.Connect(context.Background(), at(0, 2))
	if err != nil {
		t.Fatal(err)
	}
	if r, err := d.Shell(context.Background(), "id"); err != nil || string(r.Stdout) != "ok\n" {
		t.Fatalf("%q %v", r.Stdout, err)
	}
	if !contains(s.log(), "host:transport:ABC") || !contains(s.log(), "host-serial:ABC:features") {
		t.Fatalf("requests %q", s.log())
	}
}
