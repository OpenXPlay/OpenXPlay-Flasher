// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package profile

import "testing"

func TestN1ProfileNetworkOnly(t *testing.T) {
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
	if n1.Transport != ModeSSH || len(n1.Connections) != 1 || n1.Connections[0] != (ConnectionMode{ID: "network", Port: 22}) || len(n1.USB) != 0 || n1.Base != nil {
		t.Fatalf("%+v", n1)
	}
	if err := n1.Validate(); err != nil {
		t.Fatalf("network-only SSH profile rejected: %v", err)
	}
	base, _ := n1.Layer("base")
	device, _ := n1.Layer("device")
	if !base.ReadOnly || base.KernelRelease != "" || base.Guide != "phicommN1" || device.Package != "openxplay-phicomm-n1" {
		t.Fatal("N1 layers")
	}
	for name, mutate := range map[string]func(*Profile){
		"missing connection":   func(p *Profile) { p.Connections = nil },
		"duplicate connection": func(p *Profile) { p.Connections = append(p.Connections, p.Connections[0]) },
		"unknown connection":   func(p *Profile) { p.Connections[0].ID = "other" },
		"fixed host":           func(p *Profile) { p.Connections[0].Host = "n1.test" },
		"zero port":            func(p *Profile) { p.Connections[0].Port = 0 },
		"port too high":        func(p *Profile) { p.Connections[0].Port = 65536 },
		"missing confirmation": func(p *Profile) { p.Confirmation = nil },
		"USB signature":        func(p *Profile) { p.USB = []USBMatch{{Mode: ModeADB, VendorID: "18d1", ProductID: "d001"}} },
		"writable base":        func(p *Profile) { p.Layers[0].ReadOnly = false },
	} {
		t.Run(name, func(t *testing.T) {
			all, _ := Builtin()
			for _, p := range all {
				if p.ID == "phicomm-n1" {
					mutate(&p)
					if p.Validate() == nil {
						t.Fatal("invalid N1 accepted")
					}
				}
			}
		})
	}
}
