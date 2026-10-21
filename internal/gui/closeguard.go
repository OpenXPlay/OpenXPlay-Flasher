// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package gui

import "sync"

// closeGuard serves window close, the app menu and OS quit shortcuts. The native prompt is
// asynchronous on some platforms: cancel the original close, then retry only after approval.
type closeGuard struct {
	running  func() bool
	prompt   func(answer func(bool))
	quit     func()
	mu       sync.Mutex
	pending  bool
	approved bool
}

func (g *closeGuard) allow() bool {
	g.mu.Lock()
	if !g.running() || g.approved {
		g.approved = false
		g.mu.Unlock()
		return true
	}
	if g.pending {
		g.mu.Unlock()
		return false
	}
	g.pending = true
	g.mu.Unlock()
	g.prompt(func(quit bool) {
		g.mu.Lock()
		g.pending, g.approved = false, quit
		g.mu.Unlock()
		if quit {
			g.quit()
		}
	})
	return false
}
