// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

// Package adb talks to a device's adb daemon: through the adb server when one is running and owns
// the device, otherwise directly over USB. Callers see one Device either way.
package adb

import (
	"context"
	"errors"
	"io/fs"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/usb"
)

// Result is the outcome of a shell command.
type Result struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// Device is one connected adb device.
type Device interface {
	// Shell runs cmd with the device's shell (/bin/sh -c) and returns its output and exit code.
	// Stderr is empty when the transport cannot keep it apart from stdout.
	Shell(ctx context.Context, cmd string) (Result, error)
	// Pull reads a whole file. It fails with ErrTooLarge when the file is larger than max bytes.
	Pull(ctx context.Context, path string, max int64) ([]byte, error)
	// Push writes data to path, creating or replacing it, with the given permission bits.
	Push(ctx context.Context, path string, data []byte, mode fs.FileMode) error
	// Close releases the connection.
	Close() error
}

// Connector opens the adb device at a USB location.
type Connector interface {
	Connect(ctx context.Context, dev usb.DeviceInfo) (Device, error)
}

var (
	// ErrTooLarge: Pull found a file larger than its limit.
	ErrTooLarge = errors.New("adb: file too large")
	// ErrNoDevice: no adb device answers at that USB location.
	ErrNoDevice = errors.New("adb: no adb device at this USB port")
	// ErrBusy: another program (usually an adb server that is not reachable) holds the device.
	ErrBusy = errors.New("adb: the device is in use by another program")
)
