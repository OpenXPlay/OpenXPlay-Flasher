// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/testssh"
)

func TestCLIPrivacyRulesOverN1SSH(t *testing.T) {
	t.Setenv("OXP_FLASHER_SSH_PASSWORD", "cli-password")
	_, _, _ = engineFixture(t)
	board := testssh.NewN1(t, "cli-password")
	root := board.EnableConfig(t)
	host, port := board.Server.HostPort()
	flags := []string{"--ssh", fmt.Sprintf("riko@%s:%d", host, port), "--accept-host-key", board.Server.Fingerprint()}
	const path = "/var/lib/openxplay/public-name.json"
	input := filepath.Join(t.TempDir(), "patch.json")
	run := func(cmd, path, content string, want int) string {
		t.Helper()
		args := []string{path}
		if cmd == "config-put" {
			if err := os.WriteFile(input, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			args = append(args, input, "--restart")
		}
		var out, errout bytes.Buffer
		code := runEngineCLI(cmd, append(args, flags...), nil, &out, &errout)
		if code != want {
			t.Fatalf("%s: exit %d: %s", cmd, code, errout.String())
		}
		if strings.Contains(out.String()+errout.String(), "secret-cli-token") {
			t.Fatal("token in CLI output")
		}
		return out.String()
	}
	run("config-put", path, `{"provider":"dynv6","name":"cli.example.org","token":"secret-cli-token","enabled":true}`, 0)
	status := run("config-get", path, "", 0)
	var view map[string]any
	if json.Unmarshal([]byte(status), &view) != nil || view["tokenSet"] != true || view["agreeLetsEncryptTerms"] != false {
		t.Fatal("wrong redacted status")
	}
	if _, ok := view["token"]; ok {
		t.Fatal("raw token key exposed")
	}
	run("config-put", path, `{"token":"","agreeLetsEncryptTerms":true}`, 0)
	data, _ := os.ReadFile(root + path)
	if !strings.Contains(string(data), "secret-cli-token") || !strings.Contains(string(data), `"agreeLetsEncryptTerms": true`) {
		t.Fatal("empty token or explicit agreement lost")
	}
	run("config-put", path, `{"enabled":false}`, 0)
	data, _ = os.ReadFile(root + path)
	if !strings.Contains(string(data), `"agreeLetsEncryptTerms": true`) {
		t.Fatal("omitted consent changed")
	}
	before := string(data)
	for _, bad := range []string{`{"token":"secret-cli-token bad"}`, `{"secret-cli-token":true}`, `{"token":"secret-cli-token"} {}`, `{"token":`, `null`} {
		run("config-put", path, bad, 1)
		data, _ := os.ReadFile(root + path)
		if string(data) != before {
			t.Fatal("invalid private write modified file")
		}
	}
	alias := root + "/alias.json"
	if err := os.Symlink(root+path, alias); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{alias, "/var/lib/openxplay/public-name/cert.pem", "/var/lib/openxplay-flasher/backups/x", "/var/lib/openxplay-flasher/staging/x"} {
		run("config-get", p, "", 1)
		run("config-put", p, `{"token":"secret-cli-token"}`, 1)
	}
	for _, cmd := range board.Commands {
		if strings.Contains(cmd, "systemctl restart") || strings.Contains(cmd, "secret-cli-token") {
			t.Fatal("Privacy restart or token in command log")
		}
	}
	for _, p := range []string{"/var/lib/openxplay/settings.json", "/etc/openxplay/host.json"} {
		data, _ := os.ReadFile(root + p)
		if string(data) != "{}" {
			t.Fatal("Privacy changed another category")
		}
	}
}
