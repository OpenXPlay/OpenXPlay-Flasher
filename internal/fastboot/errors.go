// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package fastboot

import (
	"errors"
	"fmt"
)

var (
	// ErrBroken means an exchange was interrupted or malformed. The device may still be in its
	// data or write phase; open a new client before sending another command.
	ErrBroken = errors.New("fastboot: the connection is no longer usable")
	// ErrClosed means Close has released the USB interface.
	ErrClosed = errors.New("fastboot: the connection is closed")
)

// FailError is a bootloader's FAIL reply. A refusal completes the command and leaves the client
// usable, unlike a timeout or a malformed reply.
type FailError struct {
	Command, Message string
	Info             []string
}

func (e *FailError) Error() string {
	return fmt.Sprintf("fastboot: %s: %s", e.Command, e.Message)
}

// OpError identifies an interrupted operation. Unwrap preserves context.Canceled,
// context.DeadlineExceeded and underlying I/O errors for errors.Is.
type OpError struct {
	Command, Phase string
	Info           []string
	Err            error
}

func (e *OpError) Error() string {
	return fmt.Sprintf("fastboot: %s (%s): %v", e.Command, e.Phase, e.Err)
}

func (e *OpError) Unwrap() error { return e.Err }

// CommandError rejects a command or argument before that command reaches the device.
type CommandError struct{ Command, Message string }

func (e *CommandError) Error() string {
	return fmt.Sprintf("fastboot: invalid command %q: %s", e.Command, e.Message)
}

// ProtocolError describes a reply that cannot safely be interpreted as fastboot.
type ProtocolError struct{ Command, Message string }

func (e *ProtocolError) Error() string {
	return fmt.Sprintf("fastboot: %s: %s", e.Command, e.Message)
}

// ValueError is a size getvar that is empty or is not a nonnegative decimal or 0x-prefixed integer.
// An empty partition-size value is common on stock LK and need not prevent flashing.
type ValueError struct{ Name, Value string }

func (e *ValueError) Error() string {
	return fmt.Sprintf("fastboot: getvar %s has invalid size %q", e.Name, e.Value)
}

// ImageError describes malformed sparse input, with a byte offset in the source file.
type ImageError struct {
	Offset  int64
	Message string
}

func (e *ImageError) Error() string {
	return fmt.Sprintf("fastboot: image at byte %d: %s", e.Offset, e.Message)
}

// PieceLimitError means a sparse header and the next block cannot fit in Limit bytes.
type PieceLimitError struct{ Limit, Minimum int64 }

func (e *PieceLimitError) Error() string {
	return fmt.Sprintf("fastboot: piece limit %d is too small (need at least %d bytes)", e.Limit, e.Minimum)
}

// PartitionSizeError prevents the first download when an image would exceed a known partition.
type PartitionSizeError struct {
	Partition                string
	ImageSize, PartitionSize int64
}

func (e *PartitionSizeError) Error() string {
	return fmt.Sprintf("fastboot: image of %d bytes exceeds %s (%d bytes)", e.ImageSize, e.Partition, e.PartitionSize)
}
