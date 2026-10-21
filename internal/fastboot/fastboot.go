// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

// Package fastboot speaks the bootloader protocol directly over USB and streams Android sparse
// images in bounded pieces. It does not invoke platform-tools or discover devices itself.
package fastboot

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/usb"
)

// Interface is the fastboot interface class triple ff/42/03.
var Interface = usb.Interface{Class: 0xff, SubClass: 0x42, Protocol: 0x03}

const (
	commandTimeout = 10 * time.Second
	flashTimeout   = 15 * time.Minute
	transferSize   = 1 << 20
	maxDownload    = int64(1<<32 - 1) // DATA has exactly eight hexadecimal digits
)

// Response contains the final OKAY message and the INFO/TEXT lines in arrival order. A FAIL
// returns the same lines both here (for OEM) and in FailError.
type Response struct {
	Message string
	Info    []string
}

// Event reports bytes transferred for one piece, numbered from 1. Writing has no byte-level
// acknowledgement, so it reports 0 before flash and Total after OKAY. Info contains any collected
// INFO/TEXT lines on completion. Callbacks run inside the operation and must not call the client.
type Event struct {
	Phase         string // "sending", "writing", or "receiving"
	Piece, Pieces int
	Done, Total   int64
	Info          []string
}

// Progress receives synchronous progress notifications; nil disables them.
type Progress func(Event)

// Client owns one claimed USB interface. Calls, including an entire multi-piece flash, take
// turns so another command cannot steal a pending status or replace downloaded data.
type Client struct {
	bulk usb.Bulk

	mu     sync.Mutex
	active bool
	idle   chan struct{}

	// The active call owns these fields until unlock. The mutex also orders ownership changes.
	closed, broken bool
	maxRead        bool
	maxSize        int64
	maxErr         error
}

// Open claims the given device's fastboot interface. It sends nothing until the first command.
func Open(ctx context.Context, dev usb.DeviceInfo) (*Client, error) {
	if err := ctx.Err(); err != nil {
		return nil, &OpError{Phase: "opening", Err: err}
	}
	b, err := usb.OpenBulk(ctx, dev, Interface)
	if err != nil {
		return nil, &OpError{Phase: "opening", Err: err}
	}
	return New(b), nil
}

// New takes ownership of b without sending a handshake; fastboot has none.
func New(b usb.Bulk) *Client { return &Client{bulk: b} }

// lock serialises calls with a mutex, allowing callers waiting their turn to cancel too.
func (c *Client) lock(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return &OpError{Phase: "waiting", Err: err}
		}
		c.mu.Lock()
		if !c.active {
			c.active = true
			c.idle = make(chan struct{})
			c.mu.Unlock()
			return nil
		}
		idle := c.idle
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			return &OpError{Phase: "waiting", Err: ctx.Err()}
		case <-idle:
		}
	}
}

func (c *Client) unlock() {
	c.mu.Lock()
	c.active = false
	close(c.idle)
	c.mu.Unlock()
}

func (c *Client) ready(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return &OpError{Phase: "starting", Err: err}
	}
	if c.closed {
		return ErrClosed
	}
	if c.broken {
		return ErrBroken
	}
	return nil
}

// GetVar returns an OKAY value verbatim, including the empty values stock LK uses for unknown
// variables. INFO and TEXT are status packets, not part of the value.
func (c *Client) GetVar(ctx context.Context, name string) (string, error) {
	if err := c.lock(ctx); err != nil {
		return "", err
	}
	defer c.unlock()
	return c.getVar(ctx, name)
}

func (c *Client) getVar(ctx context.Context, name string) (string, error) {
	if name == "" {
		return "", &CommandError{Command: "getvar:", Message: "empty variable name"}
	}
	r, err := c.command(ctx, "getvar:"+name, commandTimeout)
	return r.Message, err
}

// MaxDownloadSize reads max-download-size once per client, accepting decimal and 0x-prefixed
// values. A failed lookup is cached too; no data is sent without a known limit.
func (c *Client) MaxDownloadSize(ctx context.Context) (int64, error) {
	if err := c.lock(ctx); err != nil {
		return 0, err
	}
	defer c.unlock()
	return c.maxDownloadSize(ctx)
}

func (c *Client) maxDownloadSize(ctx context.Context) (int64, error) {
	if err := c.ready(ctx); err != nil {
		return 0, err
	}
	if !c.maxRead {
		value, err := c.getVar(ctx, "max-download-size")
		if err == nil {
			c.maxSize, err = parseSize("max-download-size", value)
			if err == nil && c.maxSize == 0 {
				err = &ValueError{Name: "max-download-size", Value: value}
			}
		}
		c.maxRead, c.maxErr = true, err
	}
	return c.maxSize, c.maxErr
}

// PartitionSize reads a partition's expanded capacity. An empty or unknown value is an error;
// Flash treats empty OKAY values and FAIL refusals as an unavailable optional check.
func (c *Client) PartitionSize(ctx context.Context, partition string) (int64, error) {
	if err := c.lock(ctx); err != nil {
		return 0, err
	}
	defer c.unlock()
	return c.partitionSize(ctx, partition)
}

func (c *Client) partitionSize(ctx context.Context, partition string) (int64, error) {
	if partition == "" {
		return 0, &CommandError{Command: "getvar:partition-size:", Message: "empty partition name"}
	}
	name := "partition-size:" + partition
	value, err := c.getVar(ctx, name)
	if err != nil {
		return 0, err
	}
	return parseSize(name, value)
}

func parseSize(name, value string) (int64, error) {
	s := strings.TrimSpace(value)
	base := 10
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
		base, s = 16, s[2:]
	}
	n, err := strconv.ParseUint(s, base, 63)
	if err != nil {
		return 0, &ValueError{Name: name, Value: value}
	}
	return int64(n), nil
}

// OEM executes a bootloader-specific command and preserves its status text.
func (c *Client) OEM(ctx context.Context, args string) (Response, error) {
	if err := c.lock(ctx); err != nil {
		return Response{}, err
	}
	defer c.unlock()
	return c.command(ctx, "oem "+args, commandTimeout)
}

// Erase waits for a partition erase; large partitions can take several minutes on LK.
func (c *Client) Erase(ctx context.Context, partition string) error {
	if err := c.lock(ctx); err != nil {
		return err
	}
	defer c.unlock()
	if partition == "" {
		return &CommandError{Command: "erase:", Message: "empty partition name"}
	}
	_, err := c.command(ctx, "erase:"+partition, flashTimeout)
	return err
}

// Reboot asks the device to boot normally.
func (c *Client) Reboot(ctx context.Context) error { return c.reboot(ctx, "reboot") }

// RebootBootloader asks the device to restart its bootloader.
func (c *Client) RebootBootloader(ctx context.Context) error {
	return c.reboot(ctx, "reboot-bootloader")
}

func (c *Client) reboot(ctx context.Context, cmd string) error {
	if err := c.lock(ctx); err != nil {
		return err
	}
	defer c.unlock()
	_, err := c.command(ctx, cmd, commandTimeout)
	return err
}

// Close releases the interface after any active operation. Cancel that operation's context to
// interrupt it first. Repeated calls are harmless, including on a broken client.
func (c *Client) Close() error {
	if err := c.lock(context.Background()); err != nil {
		return err
	}
	defer c.unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	return c.bulk.Close()
}

func validateCommand(cmd string) error {
	if len(cmd) == 0 || len(cmd) > 64 {
		return &CommandError{Command: cmd, Message: "must contain 1 to 64 bytes"}
	}
	for i := range len(cmd) {
		if cmd[i] < 0x20 || cmd[i] > 0x7e {
			return &CommandError{Command: cmd, Message: "must be printable ASCII"}
		}
	}
	return nil
}

type status struct {
	Response
	data int64 // -1 for OKAY, otherwise the announced data phase size
}

// start bounds the whole command/status exchange, including all INFO/TEXT packets. Its context
// ends here; a following data phase uses only the caller's context.
func (c *Client) start(ctx context.Context, cmd string, wait time.Duration) (status, error) {
	if err := c.ready(ctx); err != nil {
		return status{}, err
	}
	if err := validateCommand(cmd); err != nil {
		return status{}, err
	}
	tctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	if err := c.write(tctx, cmd, "command", []byte(cmd), nil); err != nil {
		return status{}, err
	}
	return c.readStatus(tctx, cmd, nil)
}

func (c *Client) command(ctx context.Context, cmd string, wait time.Duration) (Response, error) {
	s, err := c.start(ctx, cmd, wait)
	if err == nil && s.data != -1 {
		err = c.protocolError(cmd, "unexpected DATA reply")
	}
	return s.Response, err
}

func (c *Client) finish(ctx context.Context, cmd string, info []string) (Response, error) {
	tctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	s, err := c.readStatus(tctx, cmd, info)
	if err == nil && s.data != -1 {
		err = c.protocolError(cmd, "expected a final OKAY, got DATA")
	}
	return s.Response, err
}

func (c *Client) readStatus(ctx context.Context, cmd string, info []string) (status, error) {
	s := status{Response: Response{Info: info}, data: -1}
	var buf [256]byte
	for {
		if err := ctx.Err(); err != nil {
			return s, c.interrupted(ctx, cmd, "status", s.Info, err)
		}
		n, err := c.bulk.Read(ctx, buf[:])
		if err != nil {
			return s, c.interrupted(ctx, cmd, "status", s.Info, err)
		}
		if n < 4 || n > len(buf) {
			return s, c.protocolError(cmd, fmt.Sprintf("status has %d bytes, need at least four", n))
		}
		text := string(buf[4:n])
		switch string(buf[:4]) {
		case "INFO", "TEXT":
			s.Info = append(s.Info, text)
		case "OKAY":
			s.Message = text
			return s, nil
		case "FAIL":
			s.Message = text
			return s, &FailError{Command: cmd, Message: text, Info: s.Info}
		case "DATA":
			n, err := strconv.ParseUint(text, 16, 32)
			if len(text) != 8 || err != nil {
				return s, c.protocolError(cmd, fmt.Sprintf("invalid DATA size %q", text))
			}
			s.data = int64(n)
			return s, nil
		default:
			return s, c.protocolError(cmd, fmt.Sprintf("unknown status %q", buf[:4]))
		}
	}
}

func (c *Client) protocolError(cmd, message string) error {
	c.broken = true
	return &ProtocolError{Command: cmd, Message: message}
}

func (c *Client) interrupted(ctx context.Context, cmd, phase string, info []string, err error) error {
	c.broken = true
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	return &OpError{Command: cmd, Phase: phase, Info: info, Err: err}
}

// partitionUnavailable only ignores completed replies. Transport failures and cancellations
// must stop Flash, since they leave the connection's state uncertain.
func partitionUnavailable(err error) bool {
	var fail *FailError
	var value *ValueError
	return errors.As(err, &fail) || errors.As(err, &value) && strings.TrimSpace(value.Value) == ""
}

func progress(p Progress, phase string, piece, pieces int, done, total int64, info []string) {
	if p != nil {
		p(Event{Phase: phase, Piece: piece, Pieces: pieces, Done: done, Total: total, Info: info})
	}
}
