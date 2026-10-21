// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package profile

import "testing"

func TestN1Profile(t *testing.T) {
	profiles, err := Builtin()
	if err != nil {
		t.Fatal(err)
	}
	var n1 Profile
	for _, p := range profiles {
		if p.ID == "phicomm-n1" {
			n1 = p
		}
	}
	if n1.Transport != ModeSSH || len(n1.Connections) != 2 || n1.Base != nil {
		t.Fatalf("%+v", n1)
	}
	base, _ := n1.Layer("base")
	device, _ := n1.Layer("device")
	if !base.ReadOnly || base.KernelRelease != "" || base.Guide != "phicommN1" || device.Package != "openxplay-phicomm-n1" {
		t.Fatal("N1 layers")
	}
	for _, mutate := range []func(*Profile){func(p *Profile) { p.Connections[0].Port = 0 }, func(p *Profile) { p.Confirmation = nil }, func(p *Profile) { p.USB[0].Interface = nil }, func(p *Profile) { p.Layers[0].ReadOnly = false }} {
		all, _ := Builtin()
		for _, p := range all {
			if p.ID == "phicomm-n1" {
				mutate(&p)
				if p.Validate() == nil {
					t.Fatal("invalid N1 accepted")
				}
			}
		}
	}
}
