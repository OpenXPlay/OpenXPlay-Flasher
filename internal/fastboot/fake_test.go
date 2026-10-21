// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package fastboot

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"
)

// bulkStep represents exactly one USB transfer, so unexpected ZLPs and commands interleaved
// with a data phase fail the script. No fake opens devices or invokes external programs.
type bulkStep struct {
	out        bool
	data       []byte
	err        error
	short      bool
	stall      bool
	gate       <-chan struct{}
	buffer     int
	deadline   time.Duration
	callerOnly bool
}

func out(s string) bulkStep     { return bulkStep{out: true, data: []byte(s)} }
func in(s string) bulkStep      { return bulkStep{data: []byte(s), buffer: 256, deadline: commandTimeout} }
func payload(p []byte) bulkStep { return bulkStep{out: true, data: p, callerOnly: true} }

type scriptedBulk struct {
	t      *testing.T
	mu     sync.Mutex
	steps  []bulkStep
	next   int
	closes int
}

func script(t *testing.T, steps ...bulkStep) (*Client, *scriptedBulk) {
	t.Helper()
	b := &scriptedBulk{t: t, steps: steps}
	c := New(b)
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Error(err)
		}
		b.mu.Lock()
		defer b.mu.Unlock()
		if b.next != len(b.steps) {
			t.Errorf("consumed %d of %d transfers", b.next, len(b.steps))
		}
		if b.closes != 1 {
			t.Errorf("closed bulk %d times", b.closes)
		}
	})
	return c, b
}

func (b *scriptedBulk) transfer(ctx context.Context, p []byte, write bool) (int, error) {
	b.t.Helper()
	b.mu.Lock()
	if b.next >= len(b.steps) {
		b.mu.Unlock()
		b.t.Errorf("unexpected transfer out=%v len=%d data=%q", write, len(p), p[:min(64, len(p))])
		return 0, io.ErrUnexpectedEOF
	}
	s := b.steps[b.next]
	b.next++
	b.mu.Unlock()
	if s.out != write {
		b.t.Errorf("transfer direction out=%v, want %v", write, s.out)
		return 0, io.ErrUnexpectedEOF
	}
	if s.buffer > 0 && len(p) != s.buffer {
		b.t.Errorf("read buffer %d, want %d", len(p), s.buffer)
	}
	if s.deadline > 0 {
		deadline, ok := ctx.Deadline()
		remaining := time.Until(deadline)
		if !ok || remaining > s.deadline || remaining < s.deadline-time.Second {
			b.t.Errorf("deadline in %v (present %v), want %v", remaining, ok, s.deadline)
		}
	}
	if s.callerOnly {
		if _, ok := ctx.Deadline(); ok {
			b.t.Error("a data transfer acquired an internal deadline")
		}
	}
	if write && !bytes.Equal(p, s.data) {
		b.t.Errorf("OUT len=%d %q, want len=%d %q", len(p), p[:min(64, len(p))], len(s.data), s.data[:min(64, len(s.data))])
		return 0, fmt.Errorf("unexpected OUT data")
	}
	if s.gate != nil {
		select {
		case <-s.gate:
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	if s.stall {
		<-ctx.Done()
		return 0, ctx.Err()
	}
	if s.err != nil {
		return 0, s.err
	}
	if write {
		if s.short {
			return len(p) - 1, nil
		}
		return len(p), nil
	}
	if len(s.data) > len(p) {
		b.t.Errorf("IN transfer of %d bytes exceeds buffer of %d", len(s.data), len(p))
		return 0, io.ErrShortBuffer
	}
	return copy(p, s.data), nil
}

func (b *scriptedBulk) Read(ctx context.Context, p []byte) (int, error) {
	return b.transfer(ctx, p, false)
}

func (b *scriptedBulk) Write(ctx context.Context, p []byte) (int, error) {
	return b.transfer(ctx, p, true)
}

func (b *scriptedBulk) MaxPacketSize() int { return 512 }

func (b *scriptedBulk) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closes++
	return nil
}
