// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-20

package gui

import (
	"context"
	"sync"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/app"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/device"
)

// State is what the page renders. Rev grows with every change, so the page can long-poll for the
// next one.
type State struct {
	Rev     uint64            `json:"rev"`
	Devices device.Snapshot   `json:"devices"`
	Install *app.InstallState `json:"install,omitempty"`
}

// Hub keeps the latest state pushed by the core and wakes the requests waiting for a change. It is
// the app.Events of the GUI: the page has no event channel of its own, it asks /api/state?after=.
type Hub struct {
	mu      sync.Mutex
	state   State
	changed chan struct{}
}

func NewHub() *Hub {
	return &Hub{changed: make(chan struct{})}
}

func (h *Hub) Emit(name string, data any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	switch name {
	case app.EventDevices:
		snap, ok := data.(device.Snapshot)
		if !ok {
			return
		}
		h.state.Devices = snap
	case app.EventInstall:
		state, ok := data.(app.InstallState)
		if !ok {
			return
		}
		h.state.Install = &state
	default:
		return
	}
	h.state.Rev++
	close(h.changed)
	h.changed = make(chan struct{})
}

// State returns the current state.
func (h *Hub) State() State {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.state
}

// Wait returns the state as soon as its Rev is past after, or the current state when ctx ends.
func (h *Hub) Wait(ctx context.Context, after uint64) State {
	for {
		h.mu.Lock()
		state, changed := h.state, h.changed
		h.mu.Unlock()
		if state.Rev > after {
			return state
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return state
		}
	}
}
