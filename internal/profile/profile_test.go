// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-20

package profile

import (
	"strings"
	"testing"
	"testing/fstest"
)

func TestBuiltinProfilesAreValid(t *testing.T) {
	profiles, err := Builtin()
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) == 0 || profiles[0].ID != "ufi003" {
		t.Fatalf("builtin profiles %+v", profiles)
	}
}

func TestUFI003Layers(t *testing.T) {
	profiles, _ := Builtin()
	p := profiles[0]
	relay, ok := p.Layer("relay")
	if !ok || relay.Destructive || relay.Transport != ModeADB {
		t.Fatalf("relay layer %+v", relay)
	}
	below := p.Below("relay")
	if len(below) != 2 || below[0].ID != "base" || below[1].ID != "device" {
		t.Fatalf("layers below relay %+v", below)
	}
	if len(p.Below("base")) != 0 || p.Below("nothing") != nil {
		t.Fatal("Below on the bottom or a missing layer")
	}
}

func TestLoadRejectsBadProfiles(t *testing.T) {
	good := `{"id":"x","family":"f","name":{"en":"X","zh-CN":"X"},
		"usb":[{"mode":"adb","vendorId":"18d1","productId":"d001"}],
		"layers":[{"id":"base","name":{"en":"B","zh-CN":"B"},"transport":"fastboot","destructive":true},
		          {"id":"top","name":{"en":"T","zh-CN":"T"},"transport":"adb"}]}`
	cases := map[string]string{
		"missing zh":     strings.Replace(good, `"name":{"en":"X","zh-CN":"X"}`, `"name":{"en":"X"}`, 1),
		"bad hex":        strings.Replace(good, `"18d1"`, `"18g1"`, 1),
		"unknown mode":   strings.Replace(good, `"mode":"adb"`, `"mode":"serial"`, 1),
		"repeated layer": strings.Replace(good, `"id":"top"`, `"id":"base"`, 1),
		"destructive on top": strings.Replace(strings.Replace(good, `"destructive":true}`, `"destructive":false}`, 1),
			`"transport":"adb"}`, `"transport":"adb","destructive":true}`, 1),
		"no destructive layer": strings.Replace(good, `"destructive":true}`, `"destructive":false}`, 1),
	}
	for name, body := range cases {
		_, err := Load(fstest.MapFS{"p/x.json": {Data: []byte(body)}}, "p")
		if name == "no destructive layer" {
			if err != nil {
				t.Errorf("%s: %v", name, err)
			}
			continue
		}
		if err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := Load(fstest.MapFS{"p/other.json": {Data: []byte(good)}}, "p"); err == nil {
		t.Error("file name not matching the id accepted")
	}
}
