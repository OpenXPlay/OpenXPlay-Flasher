// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package adb

import (
	"context"
	"crypto/rsa"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/usb"
)

// The native transport speaks to adbd over the bulk endpoints of the adb interface (ff/42/01).
// A message is a 24-byte little-endian header (command, arg0, arg1, data length, data checksum,
// magic = command ^ 0xffffffff), its data following in a transfer of its own. The host sends
// CNXN, answers AUTH tokens, then opens streams: OPEN(local, 0, service) is answered by
// OKAY(remote, local) or CLSE; each WRTE is acknowledged by an OKAY before the next one on that
// stream; CLSE ends a stream.
const (
	cmdCNXN = 0x4e584e43
	cmdAUTH = 0x48545541
	cmdOPEN = 0x4e45504f
	cmdOKAY = 0x59414b4f
	cmdCLSE = 0x45534c43
	cmdWRTE = 0x45545257
	cmdSTLS = 0x534c5453

	authToken        = 1
	authSignature    = 2
	authRSAPublicKey = 3

	// Peers from versionSkipChecksum on accept a zero data checksum.
	versionSkipChecksum = 0x01000001
	headerSize          = 24
	hostMaxData         = 256 << 10 // largest message data we take, announced in CNXN
	maxReadData         = 1 << 20   // largest message data we accept from any device
	legacyMaxData       = 4096      // what devices before version 0x01000001 take

	replyTimeout = 10 * time.Second // for each handshake reply
	authTimeout  = 60 * time.Second // for the user to accept the key on the device
	writeTimeout = 10 * time.Second // for each message written
)

var (
	adbInterface = usb.Interface{Class: 0xff, SubClass: 0x42, Protocol: 0x01}
	hostBanner   = []byte("host::features=shell_v2")
)

type message struct {
	command, arg0, arg1 uint32
	data                []byte
}

func checksum(data []byte) uint32 {
	var sum uint32
	for _, b := range data {
		sum += uint32(b)
	}
	return sum
}

// header encodes m's header; the checksum is left 0 unless withChecksum.
func (m message) header(withChecksum bool) []byte {
	le := binary.LittleEndian
	h := make([]byte, headerSize)
	le.PutUint32(h[0:], m.command)
	le.PutUint32(h[4:], m.arg0)
	le.PutUint32(h[8:], m.arg1)
	le.PutUint32(h[12:], uint32(len(m.data)))
	if withChecksum {
		le.PutUint32(h[16:], checksum(m.data))
	}
	le.PutUint32(h[20:], m.command^0xffffffff)
	return h
}

// parseHeader decodes a header into a message without data, its data length and its checksum.
func parseHeader(h []byte) (message, uint32, uint32, error) {
	le := binary.LittleEndian
	m := message{command: le.Uint32(h[0:]), arg0: le.Uint32(h[4:]), arg1: le.Uint32(h[8:])}
	if le.Uint32(h[20:]) != m.command^0xffffffff {
		return message{}, 0, 0, fmt.Errorf("adb: bad message magic in %x", h[:headerSize])
	}
	return m, le.Uint32(h[12:]), le.Uint32(h[16:]), nil
}

// parseBanner returns the features in a device's CNXN banner,
// "device::ro.product.name=...;...;features=shell_v2,cmd".
func parseBanner(banner []byte) []string {
	return splitList(bannerProperties(banner)["features"])
}

// USBConnector talks to adbd directly over USB, for when no adb server holds the device. Windows
// uses WinUSB without cgo; other platforms use libusb when cgo is enabled. Builds without either
// backend report usb.ErrTransfersUnsupported.
type USBConnector struct {
	// KeyPath is the adb private key (PEM); empty means <user config dir>/OpenXPlay Flasher/adbkey.
	// It is created on the first device that asks for authentication.
	KeyPath string
	// Open opens the adb interface; nil means usb.OpenBulk.
	Open func(ctx context.Context, dev usb.DeviceInfo, want usb.Interface) (usb.Bulk, error)
}

func (c USBConnector) Connect(ctx context.Context, dev usb.DeviceInfo) (Device, error) {
	if len(dev.Interfaces) > 0 && !dev.HasInterface(adbInterface) {
		return nil, fmt.Errorf("%w (%s at %s has no adb interface)", ErrNoDevice, dev.ID(), dev.Location())
	}
	open := c.Open
	if open == nil {
		open = usb.OpenBulk
	}
	bulk, err := open(ctx, dev, adbInterface)
	switch {
	case errors.Is(err, usb.ErrBusy):
		return nil, fmt.Errorf("%w (%v)", ErrBusy, err)
	case errors.Is(err, usb.ErrNotFound):
		return nil, fmt.Errorf("%w (%v)", ErrNoDevice, err)
	case err != nil:
		return nil, fmt.Errorf("adb: %w", err)
	}
	t := newUSBTransport(bulk)
	features, err := t.handshake(ctx, c.loadKey)
	if err != nil {
		bulk.Close()
		return nil, callErr(ctx, err)
	}
	t.start()
	d := newDevice(t, features)
	d.metadata = t.metadata
	d.serial = dev.Serial
	if reader, ok := bulk.(interface{ SerialNumber() (string, error) }); ok {
		if serial, err := reader.SerialNumber(); err == nil && serial != "" {
			d.serial = serial
		}
	}
	return d, nil
}

func (c USBConnector) loadKey() (*rsa.PrivateKey, error) {
	path := c.KeyPath
	if path == "" {
		var err error
		if path, err = defaultKeyPath(); err != nil {
			return nil, err
		}
	}
	key, err := loadOrCreateKey(path)
	if err != nil {
		return nil, fmt.Errorf("adb: key %s: %w", path, err)
	}
	return key, nil
}

// usbTransport multiplexes streams over one claimed adb interface. One goroutine reads messages
// and hands them to their streams; writers take turns on the OUT endpoint, one whole message
// each.
type usbTransport struct {
	bulk     usb.Bulk
	packet   int
	hdr      []byte   // the reader's header buffer
	metadata Metadata // immutable after the handshake

	wmu        sync.Mutex
	checksum   bool // send data checksums: the device is older than versionSkipChecksum
	maxData    int  // largest message data the device takes
	bulkClosed bool

	mu      sync.Mutex
	streams map[uint32]*usbStream
	nextID  uint32
	err     error
	done    chan struct{} // closed with err set: the transport is unusable

	stop       context.CancelFunc
	readerDone chan struct{}
}

func newUSBTransport(bulk usb.Bulk) *usbTransport {
	packet := bulk.MaxPacketSize()
	return &usbTransport{
		bulk:     bulk,
		packet:   packet,
		hdr:      make([]byte, max(headerSize, packet, 1024)),
		checksum: true,
		maxData:  legacyMaxData,
		streams:  map[uint32]*usbStream{},
		done:     make(chan struct{}),
	}
}

// handshake connects and authenticates: the token is signed with our key; when the device
// rejects the signature it gets the public key and asks its user.
func (t *usbTransport) handshake(ctx context.Context, loadKey func() (*rsa.PrivateKey, error)) ([]string, error) {
	if err := t.send(ctx, message{command: cmdCNXN, arg0: versionSkipChecksum, arg1: hostMaxData, data: hostBanner}); err != nil {
		return nil, err
	}
	var key *rsa.PrivateKey
	signed, offered := false, false
	for {
		wait := replyTimeout
		if offered {
			wait = authTimeout
		}
		rctx, cancel := context.WithTimeout(ctx, wait)
		m, err := t.readMessage(rctx)
		cancel()
		if err != nil {
			if offered && ctx.Err() == nil && errors.Is(err, context.DeadlineExceeded) {
				return nil, errors.New("adb: the device did not accept this computer's key; allow USB debugging on it and try again")
			}
			return nil, err
		}
		switch m.command {
		case cmdCNXN:
			props := bannerProperties(m.data)
			t.metadata = Metadata{Serial: props["ro.serialno"], Product: props["ro.product.name"],
				Model: props["ro.product.model"], Device: props["ro.product.device"]}
			t.checksum = m.arg0 < versionSkipChecksum
			if m.arg1 != 0 {
				t.maxData = min(int(m.arg1), hostMaxData)
			}
			return parseBanner(m.data), nil
		case cmdSTLS:
			return nil, errors.New("adb: the device asks for TLS, which this client does not support")
		case cmdAUTH:
			if m.arg0 != authToken || offered {
				continue
			}
			if key == nil {
				if key, err = loadKey(); err != nil {
					return nil, err
				}
			}
			reply := message{command: cmdAUTH}
			if !signed {
				reply.arg0 = authSignature
				if reply.data, err = signToken(key, m.data); err != nil {
					return nil, err
				}
				signed = true
			} else {
				reply.arg0 = authRSAPublicKey
				if reply.data, err = androidPublicKey(&key.PublicKey); err != nil {
					return nil, err
				}
				offered = true
			}
			if err := t.send(ctx, reply); err != nil {
				return nil, err
			}
		}
		// Anything else is left over from an earlier session.
	}
}

// readMessage reads one message. A header comes as a short transfer (any data in the same
// transfer is kept); the data is read with transfers of exactly the missing size; zero-length
// packets ending earlier data are skipped. A zero checksum is accepted, any other must match.
func (t *usbTransport) readMessage(ctx context.Context) (message, error) {
	for {
		n, err := t.bulk.Read(ctx, t.hdr)
		if err != nil {
			return message{}, err
		}
		if n == 0 {
			continue
		}
		if n < headerSize {
			return message{}, fmt.Errorf("adb: short message header of %d bytes", n)
		}
		m, length, sum, err := parseHeader(t.hdr[:headerSize])
		if err != nil {
			return message{}, err
		}
		if length > maxReadData {
			return message{}, fmt.Errorf("adb: message data of %d bytes", length)
		}
		m.data = make([]byte, length)
		got := copy(m.data, t.hdr[headerSize:n])
		for got < len(m.data) {
			k, err := t.bulk.Read(ctx, m.data[got:])
			if err != nil {
				return message{}, err
			}
			got += k
		}
		if sum != 0 && sum != checksum(m.data) {
			return message{}, errors.New("adb: message checksum mismatch")
		}
		return m, nil
	}
}

// send writes one message: the header, the data, and a zero-length packet when the data fills
// its last packet. The context is checked before starting; a started message is finished (within
// writeTimeout) so the stream of messages stays whole. A failed write ends the transport.
func (t *usbTransport) send(ctx context.Context, m message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	t.wmu.Lock()
	defer t.wmu.Unlock()
	if t.bulkClosed {
		return errClosed
	}
	if err := t.failure(); err != nil {
		return err
	}
	wctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	write := func(p []byte) error {
		n, err := t.bulk.Write(wctx, p)
		if err == nil && n != len(p) {
			err = io.ErrShortWrite
		}
		return err
	}
	err := write(m.header(t.checksum))
	if err == nil && len(m.data) > 0 {
		err = write(m.data)
		if err == nil && t.packet > 0 && len(m.data)%t.packet == 0 {
			err = write(nil)
		}
	}
	if err != nil {
		err = fmt.Errorf("adb: USB write: %w", err)
		t.fail(err)
	}
	return err
}

func (t *usbTransport) start() {
	ctx, cancel := context.WithCancel(context.Background())
	t.stop, t.readerDone = cancel, make(chan struct{})
	go t.run(ctx)
}

func (t *usbTransport) run(ctx context.Context) {
	defer close(t.readerDone)
	for {
		m, err := t.readMessage(ctx)
		if err != nil {
			t.fail(fmt.Errorf("adb: USB read: %w", err))
			return
		}
		t.dispatch(m)
	}
}

func (t *usbTransport) dispatch(m message) {
	switch m.command {
	case cmdOKAY, cmdWRTE, cmdCLSE:
	case cmdCNXN, cmdAUTH:
		t.fail(errors.New("adb: the device restarted the connection"))
		return
	default:
		return
	}
	t.mu.Lock()
	s := t.streams[m.arg1]
	if s != nil && m.command == cmdCLSE {
		delete(t.streams, m.arg1)
	}
	t.mu.Unlock()
	if s == nil {
		// A stream we gave up on: close it on the device too.
		if m.command != cmdCLSE && m.arg0 != 0 {
			go t.send(context.Background(), message{command: cmdCLSE, arg0: m.arg1, arg1: m.arg0})
		}
		return
	}
	s.deliver(m)
}

func (t *usbTransport) fail(err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.err == nil {
		t.err = err
		close(t.done)
	}
}

func (t *usbTransport) failure() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.err
}

func (t *usbTransport) forget(local uint32) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.streams, local)
}

func (t *usbTransport) open(ctx context.Context, name string) (service, error) {
	req := append([]byte(name), 0)
	if len(req) > t.maxData {
		return nil, fmt.Errorf("adb: request of %d bytes exceeds the device's limit of %d", len(req), t.maxData)
	}
	s := &usbStream{t: t, ctx: ctx, notify: make(chan struct{}, 1)}
	t.mu.Lock()
	if t.err != nil {
		t.mu.Unlock()
		return nil, t.err
	}
	t.nextID++
	for t.nextID == 0 || t.streams[t.nextID] != nil {
		t.nextID++
	}
	s.local = t.nextID
	t.streams[s.local] = s
	t.mu.Unlock()
	if err := t.send(ctx, message{command: cmdOPEN, arg0: s.local, data: req}); err != nil {
		t.forget(s.local)
		return nil, err
	}
	for {
		s.mu.Lock()
		opened, eof := s.opened, s.eof
		s.mu.Unlock()
		switch {
		case opened:
			return s, nil
		case eof:
			return nil, fmt.Errorf("adb: the device refused %q", serviceKind(name))
		}
		if err := s.wait(); err != nil {
			s.Close()
			return nil, err
		}
	}
}

// serviceKind is a service name without its arguments, for messages.
func serviceKind(name string) string {
	kind, _, _ := strings.Cut(name, ":")
	return kind + ":"
}

// close stops the reader, then releases the interface once no message is being written.
func (t *usbTransport) close() error {
	t.fail(errClosed)
	if t.stop != nil {
		t.stop()
		<-t.readerDone
	}
	t.wmu.Lock()
	defer t.wmu.Unlock()
	t.bulkClosed = true
	return t.bulk.Close()
}

// usbStream is one service on the native transport. Its reads and writes run on the goroutine of
// the call that opened it, bound to that call's context.
type usbStream struct {
	t      *usbTransport
	ctx    context.Context
	local  uint32
	notify chan struct{} // signalled on every change below

	mu     sync.Mutex
	remote uint32
	opened bool
	acks   int      // OKAYs for our WRTEs not yet waited for
	queue  [][]byte // WRTE data not yet read
	eof    bool     // the device closed the stream
	closed bool

	pending []byte // data being read
}

func (s *usbStream) deliver(m message) {
	s.mu.Lock()
	switch m.command {
	case cmdOKAY:
		if !s.opened {
			s.opened, s.remote = true, m.arg0
		} else {
			s.acks++
		}
	case cmdWRTE:
		s.queue = append(s.queue, m.data)
	case cmdCLSE:
		s.eof = true
	}
	s.mu.Unlock()
	select {
	case s.notify <- struct{}{}:
	default:
	}
}

// wait blocks until the stream changes, the call's context ends or the transport fails.
func (s *usbStream) wait() error {
	select {
	case <-s.notify:
		return nil
	case <-s.ctx.Done():
		return s.ctx.Err()
	case <-s.t.done:
		return s.t.failure()
	}
}

// Read returns queued data, acknowledging each WRTE as it is taken so the device sends the next.
func (s *usbStream) Read(p []byte) (int, error) {
	for len(s.pending) == 0 {
		s.mu.Lock()
		if len(s.queue) > 0 {
			s.pending, s.queue = s.queue[0], s.queue[1:]
			remote := s.remote
			s.mu.Unlock()
			if err := s.t.send(s.ctx, message{command: cmdOKAY, arg0: s.local, arg1: remote}); err != nil {
				return 0, err
			}
			continue
		}
		eof := s.eof
		s.mu.Unlock()
		if eof {
			return 0, io.EOF
		}
		if err := s.wait(); err != nil {
			return 0, err
		}
	}
	n := copy(p, s.pending)
	s.pending = s.pending[n:]
	return n, nil
}

// Write sends p in messages the device takes, each after the previous one is acknowledged.
func (s *usbStream) Write(p []byte) (int, error) {
	written := 0
	for written < len(p) {
		chunk := p[written:min(len(p), written+s.t.maxData)]
		s.mu.Lock()
		eof, remote := s.eof, s.remote
		s.mu.Unlock()
		if eof {
			return written, io.ErrClosedPipe
		}
		if err := s.t.send(s.ctx, message{command: cmdWRTE, arg0: s.local, arg1: remote, data: chunk}); err != nil {
			return written, err
		}
		for acked := false; !acked; {
			s.mu.Lock()
			acked, eof = s.acks > 0, s.eof
			if acked {
				s.acks--
			}
			s.mu.Unlock()
			if acked {
				break
			}
			if eof {
				return written, io.ErrClosedPipe
			}
			if err := s.wait(); err != nil {
				return written, err
			}
		}
		written += len(chunk)
	}
	return written, nil
}

// Close closes the stream on the device unless the device closed it first, even when the call's
// context has ended, so adbd ends the service.
func (s *usbStream) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	eof, opened, remote := s.eof, s.opened, s.remote
	s.mu.Unlock()
	s.t.forget(s.local)
	if eof || !opened {
		return nil
	}
	err := s.t.send(context.Background(), message{command: cmdCLSE, arg0: s.local, arg1: remote})
	if errors.Is(err, errClosed) {
		return nil
	}
	return err
}
