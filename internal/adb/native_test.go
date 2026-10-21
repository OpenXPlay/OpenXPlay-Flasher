// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package adb

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"net"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/usb"
)

func TestMessageHeader(t *testing.T) {
	m := message{command: cmdCNXN, arg0: 0x01000001, arg1: 4096, data: []byte("host::")}
	want := "434e584e" + "01000001" + "00100000" + "06000000" + "32020000" + "bcb1a7b1"
	if got := hex.EncodeToString(m.header(true)); got != want {
		t.Fatalf("header\n got %s\nwant %s", got, want)
	}
	if got := hex.EncodeToString(m.header(false)); got != want[:32]+"00000000"+want[40:] {
		t.Fatalf("header without checksum %s", got)
	}
	back, n, sum, err := parseHeader(m.header(true))
	if err != nil || back.command != cmdCNXN || back.arg0 != m.arg0 || back.arg1 != m.arg1 || n != 6 || sum != 0x232 {
		t.Fatalf("parseHeader: %+v %d %#x %v", back, n, sum, err)
	}
	bad := m.header(true)
	bad[20] ^= 1
	if _, _, _, err := parseHeader(bad); err == nil {
		t.Fatal("bad magic accepted")
	}
	if checksum([]byte{0xff, 0xff, 1}) != 0x1ff {
		t.Fatal("checksum")
	}
	for name, want := range map[string]uint32{"CNXN": cmdCNXN, "AUTH": cmdAUTH, "OPEN": cmdOPEN, "OKAY": cmdOKAY,
		"CLSE": cmdCLSE, "WRTE": cmdWRTE, "STLS": cmdSTLS} {
		if got := binary.LittleEndian.Uint32([]byte(name)); got != want {
			t.Errorf("%s = %#x, want %#x", name, want, got)
		}
	}
}

func TestParseBanner(t *testing.T) {
	cases := map[string][]string{
		"device::ro.product.name=occam;ro.product.model=Nexus 4;ro.product.device=mako;\x00": nil,
		"device::ro.product.name=x;features=shell_v2,cmd,stat_v2":                            {"shell_v2", "cmd", "stat_v2"},
		"device::features=shell_v2;ro.product.model=y\x00":                                   {"shell_v2"},
	}
	for banner, want := range cases {
		if got := parseBanner([]byte(banner)); !reflect.DeepEqual(got, want) {
			t.Errorf("parseBanner(%q) = %q, want %q", banner, got, want)
		}
	}
}

// fakeBulk is an in-memory endpoint pair. The host side is a usb.Bulk; a transfer larger than the
// host's buffer is read across several Reads, as its packets would be.
type fakeBulk struct {
	packet  int
	toDev   chan []byte
	toHost  chan []byte
	closed  chan struct{}
	once    sync.Once
	pending []byte
}

var errBulkClosed = errors.New("fake bulk closed")

func newFakeBulk(packet int) *fakeBulk {
	return &fakeBulk{packet: packet, toDev: make(chan []byte, 1024), toHost: make(chan []byte, 1024), closed: make(chan struct{})}
}

func (b *fakeBulk) Read(ctx context.Context, p []byte) (int, error) {
	if len(b.pending) == 0 {
		select {
		case tr := <-b.toHost:
			if len(tr) == 0 {
				return 0, nil
			}
			b.pending = tr
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-b.closed:
			return 0, errBulkClosed
		}
	}
	n := copy(p, b.pending)
	b.pending = b.pending[n:]
	return n, nil
}

func (b *fakeBulk) Write(ctx context.Context, p []byte) (int, error) {
	select {
	case b.toDev <- append([]byte{}, p...):
		return len(p), nil
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-b.closed:
		return 0, errBulkClosed
	}
}

func (b *fakeBulk) MaxPacketSize() int { return b.packet }

func (b *fakeBulk) Close() error {
	b.once.Do(func() { close(b.closed) })
	return nil
}

// fakeADBD is an adbd on the device side of a fakeBulk, checking what the host sends: header and
// data transfers, zero-length packets, checksums, message sizes and flow control.
type fakeADBD struct {
	t       *testing.T
	b       *fakeBulk
	dev     *fakeDevice
	version uint32
	maxData int
	banner  string
	auth    bool // ask for authentication
	stale   bool // send a message left from an earlier session before answering
	merge   bool // send header and data in one transfer

	mu        sync.Mutex
	keys      []*rsa.PublicKey
	token     []byte
	connected bool
	streams   map[uint32]*fakeStream
	nextID    uint32
	signedIn  int // connections accepted by signature
	keyed     int // connections accepted by public key
	closes    int // CLSE from the host
	wmu       sync.Mutex
}

type fakeStream struct {
	local, remote uint32
	conn          net.Conn // the host's end of the service pipe
	acks          chan struct{}
	in            chan []byte
	done          chan struct{}
}

func (a *fakeADBD) errorf(format string, args ...any) {
	a.t.Errorf("fake adbd: "+format, args...)
}

func (a *fakeADBD) start() {
	a.streams = map[uint32]*fakeStream{}
	go func() {
		for {
			m, ok := a.read()
			if !ok {
				return
			}
			a.handle(m)
		}
	}()
}

func (a *fakeADBD) recv() ([]byte, bool) {
	select {
	case tr := <-a.b.toDev:
		return tr, true
	case <-a.b.closed:
		return nil, false
	}
}

func (a *fakeADBD) read() (message, bool) {
	h, ok := a.recv()
	if !ok {
		return message{}, false
	}
	if len(h) != headerSize {
		a.errorf("header transfer of %d bytes", len(h))
		return message{}, false
	}
	m, length, sum, err := parseHeader(h)
	if err != nil {
		a.errorf("%v", err)
		return message{}, false
	}
	var data []byte
	for len(data) < int(length) {
		tr, ok := a.recv()
		if !ok {
			return message{}, false
		}
		if len(tr) == 0 {
			a.errorf("zero-length packet inside data")
		}
		data = append(data, tr...)
	}
	if length > 0 && int(length)%a.b.packet == 0 {
		if tr, ok := a.recv(); !ok {
			return message{}, false
		} else if len(tr) != 0 {
			a.errorf("no zero-length packet after %d bytes", length)
		}
	}
	a.mu.Lock()
	connected := a.connected
	a.mu.Unlock()
	switch {
	case connected && len(data) > a.maxData:
		a.errorf("message of %d bytes, the device takes %d", len(data), a.maxData)
	case (!connected || a.version < versionSkipChecksum) && sum != checksum(data):
		a.errorf("checksum %#x, want %#x", sum, checksum(data))
	case connected && a.version >= versionSkipChecksum && sum != 0:
		a.errorf("checksum sent to a device that takes 0")
	}
	m.data = data
	return m, true
}

func (a *fakeADBD) send(m message) {
	a.wmu.Lock()
	defer a.wmu.Unlock()
	h := m.header(a.version < versionSkipChecksum)
	put := func(tr []byte) {
		select {
		case a.b.toHost <- tr:
		case <-a.b.closed:
		}
	}
	if a.merge {
		put(append(h, m.data...))
	} else {
		put(h)
		if len(m.data) > 0 {
			put(m.data)
		}
	}
	// A transfer that fills its last packet ends with a zero-length one.
	size := len(m.data)
	if a.merge {
		size += headerSize
	}
	if len(m.data) > 0 && size%a.b.packet == 0 {
		put(nil)
	}
}

func (a *fakeADBD) connect() {
	a.mu.Lock()
	a.connected = true
	a.mu.Unlock()
	a.send(message{command: cmdCNXN, arg0: a.version, arg1: uint32(a.maxData), data: []byte(a.banner)})
}

func (a *fakeADBD) sendToken() {
	a.token = make([]byte, 20)
	rand.Read(a.token)
	a.send(message{command: cmdAUTH, arg0: authToken, data: a.token})
}

func (a *fakeADBD) handle(m message) {
	switch m.command {
	case cmdCNXN:
		if string(m.data) != "host::features=shell_v2" || m.arg0 != versionSkipChecksum || m.arg1 != hostMaxData {
			a.errorf("CNXN %#x %d %q", m.arg0, m.arg1, m.data)
		}
		if a.stale {
			a.send(message{command: cmdWRTE, arg0: 7, arg1: 99, data: []byte("left over")})
		}
		if a.auth {
			a.sendToken()
		} else {
			a.connect()
		}
	case cmdAUTH:
		switch m.arg0 {
		case authSignature:
			for _, k := range a.keys {
				if rsa.VerifyPKCS1v15(k, crypto.SHA1, a.token, m.data) == nil {
					a.signedIn++
					a.connect()
					return
				}
			}
			a.sendToken()
		case authRSAPublicKey:
			pub, err := decodeAndroidKey(m.data)
			if err != nil {
				a.errorf("public key: %v", err)
				return
			}
			// The user accepts.
			a.keys = append(a.keys, pub)
			a.keyed++
			a.connect()
		}
	case cmdOPEN:
		name, ok := strings.CutSuffix(string(m.data), "\x00")
		if !ok || m.arg0 == 0 || m.arg1 != 0 {
			a.errorf("OPEN %d %d %q", m.arg0, m.arg1, m.data)
		}
		if !strings.HasPrefix(name, "shell") && name != "sync:" {
			a.send(message{command: cmdCLSE, arg1: m.arg0})
			return
		}
		a.mu.Lock()
		a.nextID++
		devEnd, hostEnd := net.Pipe()
		s := &fakeStream{local: 100 + a.nextID, remote: m.arg0, conn: hostEnd, acks: make(chan struct{}, 1),
			in: make(chan []byte, 16), done: make(chan struct{})}
		a.streams[s.local] = s
		a.mu.Unlock()
		a.send(message{command: cmdOKAY, arg0: s.local, arg1: s.remote})
		go a.dev.serve(devEnd, name)
		go a.pumpOut(s)
		go a.pumpIn(s)
	case cmdWRTE, cmdOKAY, cmdCLSE:
		a.mu.Lock()
		s := a.streams[m.arg1]
		if m.command == cmdCLSE {
			a.closes++
			delete(a.streams, m.arg1)
		}
		a.mu.Unlock()
		if s == nil {
			return
		}
		if m.arg0 != s.remote {
			a.errorf("message for stream %d from host stream %d, want %d", m.arg1, m.arg0, s.remote)
		}
		switch m.command {
		case cmdWRTE:
			select {
			case s.in <- m.data:
			default:
				a.errorf("host wrote without waiting for OKAY")
			}
		case cmdOKAY:
			select {
			case s.acks <- struct{}{}:
			default:
				a.errorf("OKAY for no WRTE")
			}
		case cmdCLSE:
			close(s.done)
			s.conn.Close()
		}
	}
}

// pumpOut sends the service's output as WRTEs, one at a time, then CLSE when it ends.
func (a *fakeADBD) pumpOut(s *fakeStream) {
	buf := make([]byte, a.maxData)
	for {
		n, err := s.conn.Read(buf)
		if n > 0 {
			a.send(message{command: cmdWRTE, arg0: s.local, arg1: s.remote, data: append([]byte{}, buf[:n]...)})
			select {
			case <-s.acks:
			case <-s.done:
				return
			}
		}
		if err != nil {
			a.mu.Lock()
			_, open := a.streams[s.local]
			delete(a.streams, s.local)
			a.mu.Unlock()
			if open {
				a.send(message{command: cmdCLSE, arg0: s.local, arg1: s.remote})
			}
			return
		}
	}
}

// pumpIn hands the host's data to the service and acknowledges it once taken.
func (a *fakeADBD) pumpIn(s *fakeStream) {
	for {
		select {
		case data := <-s.in:
			if _, err := s.conn.Write(data); err != nil {
				return
			}
			a.send(message{command: cmdOKAY, arg0: s.local, arg1: s.remote})
		case <-s.done:
			return
		}
	}
}

func (a *fakeADBD) counts() (signedIn, keyed, closes int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.signedIn, a.keyed, a.closes
}

// decodeAndroidKey reads a public key in adbd's format, checking its precomputed values.
func decodeAndroidKey(b []byte) (*rsa.PublicKey, error) {
	text, ok := strings.CutSuffix(string(b), keyComment)
	if !ok {
		return nil, fmt.Errorf("no comment and NUL in %q", b[max(0, len(b)-40):])
	}
	raw, err := base64.StdEncoding.DecodeString(text)
	if err != nil {
		return nil, err
	}
	le := binary.LittleEndian
	if len(raw) != 4*(3+2*keyWords) || le.Uint32(raw) != keyWords {
		return nil, fmt.Errorf("key struct of %d bytes", len(raw))
	}
	little := func(b []byte) *big.Int {
		be := make([]byte, len(b))
		for i := range b {
			be[len(b)-1-i] = b[i]
		}
		return new(big.Int).SetBytes(be)
	}
	n := little(raw[8 : 8+4*keyWords])
	rr := little(raw[8+4*keyWords : 8+8*keyWords])
	if uint32(n.Uint64())*le.Uint32(raw[4:]) != 0xffffffff {
		return nil, errors.New("n0inv is not -1/n mod 2^32")
	}
	if want := new(big.Int).Exp(big.NewInt(2), big.NewInt(2*keyBits), n); rr.Cmp(want) != 0 {
		return nil, errors.New("rr is not 2^4096 mod n")
	}
	return &rsa.PublicKey{N: n, E: int(le.Uint32(raw[8+8*keyWords:]))}, nil
}

// nativeDevice connects a USBConnector to a new fake adbd serving dev.
func nativeDevice(t *testing.T, a *fakeADBD, keyPath string) Device {
	t.Helper()
	a.t = t
	a.start()
	c := USBConnector{KeyPath: keyPath, Open: func(ctx context.Context, dev usb.DeviceInfo, want usb.Interface) (usb.Bulk, error) {
		if want != (usb.Interface{Class: 0xff, SubClass: 0x42, Protocol: 0x01}) {
			t.Errorf("Open for %+v", want)
		}
		return a.b, nil
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	d, err := c.Connect(ctx, at(0, 1, 1, 1))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func oldADBD(dev *fakeDevice) *fakeADBD {
	return &fakeADBD{b: newFakeBulk(64), dev: dev, version: 0x01000000, maxData: 4096, auth: true, stale: true,
		banner: "device::ro.product.name=occam;ro.product.model=Nexus 4;ro.product.device=mako;\x00"}
}

func TestNativeOldDevice(t *testing.T) {
	dev := newFakeDevice()
	dev.commands["id"] = fakeCmd{stdout: "uid=0(root)\n"}
	dev.commands["echo out; echo err >&2; exit 3"] = fakeCmd{stdout: "out\n", stderr: "err\n", code: 3}
	big := bytes.Repeat([]byte("0123456789abcdef"), 10000)
	dev.files["/etc/openxplay/host.json"] = &fakeFile{data: []byte(`{"model":"UFI003"}`), perm: 0o644}
	dev.files["/big"] = &fakeFile{data: big, perm: 0o644}
	dev.files["/proc/cmdline"] = &fakeFile{data: bytes.Repeat([]byte("x"), 300), perm: 0o444, sizeZero: true}
	dev.failSend["/ro/file"] = "Read-only file system"
	dev.chunk = 5000 // DATA chunks over two WRTEs of 4096

	keyPath := filepath.Join(t.TempDir(), "OpenXPlay Flasher", "adbkey")
	a := oldADBD(dev)
	d := nativeDevice(t, a, keyPath)
	// An unknown key: the signature is refused, the public key is accepted.
	if signedIn, keyed, _ := a.counts(); signedIn != 0 || keyed != 1 {
		t.Fatalf("auth: signed in %d, keyed %d", signedIn, keyed)
	}
	ctx := context.Background()
	r, err := d.Shell(ctx, "echo out; echo err >&2; exit 3")
	if err != nil || string(r.Stdout) != "out\nerr\n" || len(r.Stderr) != 0 || r.ExitCode != 3 {
		t.Fatalf("shell: %+v %v", r, err)
	}
	testSync(t, d, dev, big)
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}

	// The same key again: accepted by signature alone.
	b := oldADBD(dev)
	b.keys = a.keys
	d = nativeDevice(t, b, keyPath)
	defer d.Close()
	if signedIn, keyed, _ := b.counts(); signedIn != 1 || keyed != 0 {
		t.Fatalf("second auth: signed in %d, keyed %d", signedIn, keyed)
	}
	if r, err := d.Shell(ctx, "id"); err != nil || string(r.Stdout) != "uid=0(root)\n" {
		t.Fatalf("shell: %+v %v", r, err)
	}
}

func TestNativeNewDevice(t *testing.T) {
	dev := newFakeDevice()
	dev.commands["echo out; echo err >&2; exit 3"] = fakeCmd{stdout: "out\n", stderr: "err\n", code: 3}
	big := bytes.Repeat([]byte("0123456789abcdef"), 40000) // 640000 bytes: WRTEs of 256 KiB
	dev.files["/etc/openxplay/host.json"] = &fakeFile{data: []byte(`{"model":"UFI003"}`), perm: 0o644}
	dev.files["/big"] = &fakeFile{data: big, perm: 0o644}
	dev.files["/proc/cmdline"] = &fakeFile{data: bytes.Repeat([]byte("x"), 300), perm: 0o444, sizeZero: true}
	dev.failSend["/ro/file"] = "Read-only file system"
	a := &fakeADBD{b: newFakeBulk(512), dev: dev, version: versionSkipChecksum, maxData: 1 << 20, merge: true,
		banner: "device::ro.product.name=x;features=shell_v2,cmd"}
	d := nativeDevice(t, a, filepath.Join(t.TempDir(), "adbkey"))
	defer d.Close()
	r, err := d.Shell(context.Background(), "echo out; echo err >&2; exit 3")
	if err != nil || string(r.Stdout) != "out\n" || string(r.Stderr) != "err\n" || r.ExitCode != 3 {
		t.Fatalf("shell v2: %+v %v", r, err)
	}
	testSync(t, d, dev, big)
	if !contains(dev.shells, "shell,v2,raw:echo out; echo err >&2; exit 3") {
		t.Fatalf("services %q", dev.shells)
	}
}

func TestNativeCancelAndRefuse(t *testing.T) {
	dev := newFakeDevice()
	dev.commands["sleep 60"] = fakeCmd{block: true}
	dev.commands["id"] = fakeCmd{stdout: "ok\n"}
	a := oldADBD(dev)
	a.auth = false
	d := nativeDevice(t, a, filepath.Join(t.TempDir(), "adbkey"))
	defer d.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := d.Shell(ctx, "sleep 60"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shell: %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("cancel took %v", time.Since(start))
	}
	if r, err := d.Shell(context.Background(), "id"); err != nil || string(r.Stdout) != "ok\n" {
		t.Fatalf("after cancel: %+v %v", r, err)
	}
	if _, _, closes := a.counts(); closes < 1 {
		t.Fatal("the cancelled stream was not closed on the device")
	}
	if _, err := d.(*device).open(context.Background(), "reboot:"); err == nil || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("refused service: %v", err)
	}
	// The device goes away during a call.
	go func() { time.Sleep(50 * time.Millisecond); a.b.Close() }()
	if _, err := d.Shell(context.Background(), "sleep 60"); err == nil {
		t.Fatal("Shell survived the device")
	}
	if _, err := d.Shell(context.Background(), "id"); err == nil {
		t.Fatal("Shell after the device went away")
	}
}

func TestUSBConnectorErrors(t *testing.T) {
	for _, tc := range []struct {
		open error
		want error
	}{
		{fmt.Errorf("%w (claim)", usb.ErrBusy), ErrBusy},
		{usb.ErrNotFound, ErrNoDevice},
		{usb.ErrTransfersUnsupported, usb.ErrTransfersUnsupported},
	} {
		c := USBConnector{Open: func(context.Context, usb.DeviceInfo, usb.Interface) (usb.Bulk, error) { return nil, tc.open }}
		if _, err := c.Connect(context.Background(), at(0, 1)); !errors.Is(err, tc.want) {
			t.Errorf("Open error %v: Connect = %v", tc.open, err)
		}
	}
	called := false
	c := USBConnector{Open: func(context.Context, usb.DeviceInfo, usb.Interface) (usb.Bulk, error) {
		called = true
		return nil, usb.ErrNotFound
	}}
	fastboot := at(0, 1)
	fastboot.Interfaces = []usb.Interface{{Class: 0xff, SubClass: 0x42, Protocol: 0x03}}
	if _, err := c.Connect(context.Background(), fastboot); !errors.Is(err, ErrNoDevice) || called {
		t.Fatalf("fastboot device: %v (opened %v)", err, called)
	}
}

func TestNativeTLSRefused(t *testing.T) {
	b := newFakeBulk(512)
	go func() {
		<-b.toDev // CNXN header
		<-b.toDev // banner
		b.toHost <- message{command: cmdSTLS, arg0: 0x01000000}.header(true)
	}()
	c := USBConnector{Open: func(context.Context, usb.DeviceInfo, usb.Interface) (usb.Bulk, error) { return b, nil }}
	if _, err := c.Connect(context.Background(), at(0, 1)); err == nil || !strings.Contains(err.Error(), "TLS") {
		t.Fatalf("Connect: %v", err)
	}
}
