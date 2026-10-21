// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/device"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/profile"
	sshdev "github.com/OpenXPlay/OpenXPlay-Flasher/internal/ssh"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/testssh"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/usb"
)

func TestSSHActiveDeviceAndIdentity(t *testing.T) {
	const password = "never-persist-this-password"
	board := testssh.NewN1(t, password)
	host, port := board.Server.HostPort()
	profiles, _ := profile.Builtin()
	bus := usb.NewFakeBus()
	svc, _ := NewForBus(bus, nil)
	svc.dataDir = t.TempDir()
	opts := sshdev.Options{Host: host, Port: port, User: "riko", Password: password, AcceptHostKey: board.Server.Fingerprint()}
	state, err := svc.SSHConnect(context.Background(), "network", opts)
	if err != nil {
		t.Fatal(err)
	}
	if state.Target.Profile != "phicomm-n1" || state.Identity.Serial != "021122334455" || state.NextSteps.AdminURL != "https://127.0.0.1:6975/" || state.Installed.Layers["base"] == "" {
		t.Fatalf("%+v", state)
	}
	for _, c := range state.Choices {
		if c.Destructive || c.ID == "base" {
			t.Fatal("N1 base install offered")
		}
	}
	// A UFI003 on the bus cannot redirect configuration from the explicitly selected N1.
	svc.detector = device.BusDetector{Bus: bus, Classifier: device.NewClassifier(profiles)}
	bus.Set(ufi003ADB.USB)
	svc.current.Candidates = []device.Candidate{ufi003ADB}
	target, err := svc.ConfigTarget()
	if err != nil || target.Profile != "phicomm-n1" {
		t.Fatal(target, err)
	}
	file, err := svc.ConfigRead(context.Background(), "/var/lib/openxplay/settings.json")
	if err != nil || file.Content != "{}" {
		t.Fatal(file, err)
	}
	inspected := svc.Install(context.Background())
	if inspected.Problem != nil {
		t.Fatal(inspected.Problem)
	}
	data, _ := json.Marshal([]any{svc.Info(), svc.Connection(), svc.InstallSnapshot()})
	if strings.Contains(string(data)+svc.InstallLog(), password) {
		t.Fatal("secret in state, diagnostics or log")
	}
	board.SetMAC("02:11:22:33:44:66")
	if _, err = svc.ConfigRead(context.Background(), "/var/lib/openxplay/settings.json"); err == nil {
		t.Fatal("replaced board accepted")
	}
	if err = svc.SelectProfile("ufi003"); err != nil {
		t.Fatal(err)
	}
	if svc.Connection().Device != nil {
		t.Fatal("SSH credentials retained on device switch")
	}
	board.SetModel("Another board")
	if _, err = svc.SSHConnect(context.Background(), "network", opts); err == nil {
		t.Fatal("wrong model accepted")
	}
}
