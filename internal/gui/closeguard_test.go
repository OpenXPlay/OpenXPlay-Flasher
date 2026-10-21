// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package gui

import "testing"

func TestCloseGuardOnlyArmedWhileRunning(t *testing.T) {
	running, prompts, quits := false, 0, 0
	var answer func(bool)
	g := &closeGuard{running: func() bool { return running }, prompt: func(a func(bool)) { prompts++; answer = a }, quit: func() { quits++ }}
	if !g.allow() || prompts != 0 {
		t.Fatal("idle close was guarded")
	}
	running = true
	if g.allow() || prompts != 1 {
		t.Fatal("running close was not guarded")
	}
	if g.allow() || prompts != 1 {
		t.Fatal("duplicate native prompt")
	}
	answer(false)
	if quits != 0 {
		t.Fatal("keep flashing quit the app")
	}
	if g.allow() || prompts != 2 {
		t.Fatal("quit shortcut bypassed guard")
	}
	answer(true)
	if quits != 1 || !g.allow() {
		t.Fatal("approved quit did not retry exactly once")
	}
	if g.allow() || prompts != 3 {
		t.Fatal("quit approval remained armed")
	}
	answer(false)
	running = false
	if !g.allow() || prompts != 3 {
		t.Fatal("guard remained armed after job")
	}
}

func TestQuitWarningsHaveBothLanguages(t *testing.T) {
	for _, lang := range []string{LangEnglish, LangChinese} {
		s := quitStrings(lang)
		if s.Warning == "" || s.Keep == "" || s.Quit == "" || s.Keep == s.Quit {
			t.Fatalf("%s: %+v", lang, s)
		}
	}
}
