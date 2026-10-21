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
	"slices"
	"strings"
	"sync/atomic"
	"time"
)

// service is one open adb service (shell, sync) on a device: a socket through the adb server or a
// stream of the native USB transport. Its I/O is bound to the context it was opened with.
type service interface {
	io.ReadWriteCloser
}

// transport opens services on one device; the server and the native backend each provide one.
type transport interface {
	open(ctx context.Context, name string) (service, error)
	close() error
}

// device implements Device with the shell and sync protocols over a transport.
type device struct {
	t       transport
	serial  string
	shellV2 bool
	closed  atomic.Bool
}

var errClosed = errors.New("adb: the connection is closed")

func newDevice(t transport, features []string) *device {
	return &device{t: t, shellV2: slices.Contains(features, "shell_v2")}
}

func (d *device) open(ctx context.Context, name string) (service, error) {
	if d.closed.Load() {
		return nil, errClosed
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s, err := d.t.open(ctx, name)
	return s, callErr(ctx, err)
}

func (d *device) Shell(ctx context.Context, cmd string) (Result, error) {
	if strings.TrimSpace(cmd) == "" {
		return Result{}, errors.New("adb: empty shell command")
	}
	if d.shellV2 {
		s, err := d.open(ctx, "shell,v2,raw:"+cmd)
		if err != nil {
			return Result{}, err
		}
		defer s.Close()
		r, err := runShellV2(s)
		return r, callErr(ctx, err)
	}
	s, err := d.open(ctx, "shell:"+sentinelCommand(cmd))
	if err != nil {
		return Result{}, err
	}
	defer s.Close()
	out, err := readAllMax(s, maxShellOutput)
	if err != nil {
		return Result{}, callErr(ctx, err)
	}
	return parseSentinel(out)
}

func (d *device) Pull(ctx context.Context, path string, max int64) ([]byte, error) {
	s, err := d.open(ctx, "sync:")
	if err != nil {
		return nil, err
	}
	defer s.Close()
	data, err := syncPull(s, path, max)
	return data, callErr(ctx, err)
}

// Push sends the file with its mode, then checks the mode with STAT: some adbd builds (the
// UFI003's among them) create every pushed file 0666, and chmod corrects that.
func (d *device) Push(ctx context.Context, path string, data []byte, mode fs.FileMode) error {
	s, err := d.open(ctx, "sync:")
	if err != nil {
		return err
	}
	err = syncPush(s, path, data, mode, time.Now())
	var got uint32
	if err == nil {
		got, _, _, err = syncStat(s, path)
	}
	if err == nil {
		_ = syncRequest(s, "QUIT", nil)
	}
	s.Close()
	if err != nil {
		return callErr(ctx, err)
	}
	if got&0o777 == uint32(mode.Perm()) {
		return nil
	}
	r, err := d.Shell(ctx, fmt.Sprintf("chmod %o %s", mode.Perm(), shellQuote(path)))
	if err == nil && r.ExitCode != 0 {
		err = fmt.Errorf("adb: chmod %o %s: exit status %d: %s", mode.Perm(), path, r.ExitCode,
			bytes.TrimSpace(append(r.Stdout, r.Stderr...)))
	}
	return err
}

// shellQuote quotes s as one word for /bin/sh.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func (d *device) Close() error {
	if d.closed.Swap(true) {
		return nil
	}
	return d.t.close()
}

// callErr reports an I/O failure caused by the end of the call's context as the context's error.
func callErr(ctx context.Context, err error) error {
	if err != nil && ctx.Err() != nil && !errors.Is(err, ctx.Err()) {
		return fmt.Errorf("%w (%v)", ctx.Err(), err)
	}
	return err
}

// readAllMax reads r to EOF, failing once it holds more than max bytes.
func readAllMax(r io.Reader, max int) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, int64(max)+1))
	if err == nil && len(data) > max {
		return nil, fmt.Errorf("adb: shell output exceeds %d bytes", max)
	}
	return data, err
}

// Serial identifies this already-open transport, without probing another device.
func (d *device) Serial() string { return d.serial }

func TransportSerial(d Device, fallback string) string {
	if s, ok := d.(interface{ Serial() string }); ok && s.Serial() != "" {
		return s.Serial()
	}
	return fallback
}
