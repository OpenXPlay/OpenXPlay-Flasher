// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/config"
	sshdev "github.com/OpenXPlay/OpenXPlay-Flasher/internal/ssh"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/testssh"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/usb"
)

func TestN1PrivacyOverSSH(t *testing.T) {
	board := testssh.NewN1(t, "fake-password")
	root := board.EnableConfig(t)
	host, port := board.Server.HostPort()
	svc, _ := NewForBus(usb.NewFakeBus(), nil)
	svc.dataDir = t.TempDir()
	defer svc.Stop()
	_, err := svc.SSHConnect(context.Background(), sshdev.Options{Host: host, Port: port, User: "riko", Password: "fake-password", AcceptHostKey: board.Server.Fingerprint()})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	call := func(action string, u config.PublicNameUpdate, data []byte, backup string) config.PublicNameView {
		t.Helper()
		view, _, err := svc.ConfigPrivacy(ctx, "public-name", action, u, data, backup)
		if err != nil {
			t.Fatalf("%s: %v", action, err)
		}
		encoded, _ := json.Marshal(view)
		if strings.Contains(string(encoded), "n1-private-token") {
			t.Fatal("token in returned status")
		}
		return view
	}
	if call("read", config.PublicNameUpdate{}, nil, "").Exists {
		t.Fatal("expected missing privacy file")
	}
	provider, name, enabled, agree := "dynv6", "n1.example.org", true, true
	view := call("write", config.PublicNameUpdate{Provider: &provider, Name: &name, Enabled: &enabled, Token: "n1-private-token", AgreeLetsEncryptTerms: &agree}, nil, "")
	if !view.TokenSet || !view.AgreeLetsEncryptTerms {
		t.Fatal("typed save lost fields")
	}
	p := "/var/lib/openxplay/public-name.json"
	st, err := os.Stat(root + p)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatal("private file mode", err)
	}
	agree = false
	call("write", config.PublicNameUpdate{AgreeLetsEncryptTerms: &agree}, nil, "")
	data, _ := os.ReadFile(root + p)
	if !strings.Contains(string(data), "n1-private-token") || !strings.Contains(string(data), `"agreeLetsEncryptTerms": false`) {
		t.Fatal("token preservation/explicit consent failed")
	}
	// Imports keep hidden accepted fields, and an empty token keeps the saved token.
	call("import", config.PublicNameUpdate{}, []byte(`{"version":1,"provider":"dynv6","name":"n1.example.org","token":"","acme":"staging"}`), "")
	_, backups, err := svc.ConfigPrivacy(ctx, "public-name", "backups", config.PublicNameUpdate{}, nil, "")
	if err != nil || len(backups) != 2 {
		t.Fatalf("backup list: %d, %v", len(backups), err)
	}
	if !call("restore", config.PublicNameUpdate{}, nil, backups[len(backups)-1].ID).AgreeLetsEncryptTerms {
		t.Fatal("restore lost saved agreement")
	}
	alias := root + "/private-link"
	if err := os.Symlink(root+p, alias); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{p, alias, "/var/lib/openxplay/public-name/cert.pem", "/var/lib/openxplay-flasher/backups/any", "/var/lib/openxplay-flasher/staging/any"} {
		if _, err := svc.ConfigRead(ctx, path); !errors.Is(err, config.ErrPrivacy) {
			t.Fatalf("raw read permitted: %s: %v", path, err)
		}
		if _, err := svc.ConfigWrite(ctx, path, "{}", true); !errors.Is(err, config.ErrPrivacy) {
			t.Fatalf("raw write permitted: %s: %v", path, err)
		}
	}
	call("remove", config.PublicNameUpdate{}, nil, "")
	if _, err := os.Stat(root + p); !os.IsNotExist(err) {
		t.Fatal("Remove kept privacy file")
	}
	_, backups, err = svc.ConfigPrivacy(ctx, "public-name", "backups", config.PublicNameUpdate{}, nil, "")
	if err != nil || len(backups) != 0 {
		t.Fatal("Remove kept privacy backups", err)
	}
	for _, path := range []string{"/var/lib/openxplay/settings.json", "/etc/openxplay/host.json"} {
		data, _ := os.ReadFile(root + path)
		if string(data) != "{}" {
			t.Fatal("Privacy modified another category")
		}
	}
	for _, cmd := range board.Commands {
		if strings.Contains(cmd, "systemctl restart") || strings.Contains(cmd, "n1-private-token") {
			t.Fatal("Privacy restart or token in command log")
		}
	}
}
