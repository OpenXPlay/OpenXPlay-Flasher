// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package gui

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/app"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/testssh"
)

func TestSSHConnectionAPIAndSecrets(t *testing.T) {
	f, svc, _, _, _ := installFixture(t, true)
	dir := t.TempDir()
	svc.ConfigureStorage(dir, filepath.Join(dir, "cache"), filepath.Join(dir, "backups"), &guiCatalog{})
	board := testssh.NewN1(t, "api-memory-only")
	host, port := board.Server.HostPort()
	for _, profile := range []string{"ufi003", "phicomm-n1"} {
		rec := f.do("POST", "/api/devices/select", jsonBody(map[string]string{"profile": profile}))
		if rec.Code != 200 {
			t.Fatal(rec.Code, rec.Body)
		}
	}
	body := map[string]any{"host": host, "port": port, "user": "riko", "password": "api-memory-only"}
	rec := f.do("POST", "/api/ssh/connect", jsonBody(body))
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), "ssh-host-unknown") || !strings.Contains(rec.Body.String(), board.Server.Fingerprint()) || !strings.Contains(rec.Body.String(), `"keyType":"ssh-ed25519"`) {
		t.Fatal(rec.Code, rec.Body)
	}
	body["acceptHostKey"] = board.Server.Fingerprint()
	rec = f.do("POST", "/api/ssh/connect", jsonBody(body))
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body)
	}
	var connected app.InstallState
	if err := json.Unmarshal(rec.Body.Bytes(), &connected); err != nil || connected.Target == nil || connected.Target.Device.Connection != "network" || connected.Target.Device.Host != host || connected.Target.Device.Port != port || connected.Target.Device.Serialno != "021122334455" {
		t.Fatal("network identity missing", err, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "api-memory-only") || strings.Contains(svc.InstallLog(), "api-memory-only") {
		t.Fatal("secret in state or session log")
	}
	rec = f.do("GET", "/api/connection", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"user":"riko"`) || !strings.Contains(rec.Body.String(), `"knownHosts":[`) || strings.Contains(rec.Body.String(), `"knownHosts":[]`) {
		t.Fatal(rec.Body)
	}
	data, err := os.ReadFile(f.settings.path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "api-memory-only") || strings.Contains(strings.ToLower(string(data)), "password") {
		t.Fatal("secret in settings")
	}
	rec = f.do("POST", "/api/config/read", `{"path":"/var/lib/openxplay/settings.json"}`)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"content":"{}"`) {
		t.Fatal(rec.Code, rec.Body)
	}
	signer, _ := testssh.NewSigner(t)
	board.Server.ChangeKey(signer)
	body["acceptHostKey"] = board.Server.Fingerprint()
	rec = f.do("POST", "/api/ssh/connect", jsonBody(body))
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), "ssh-host-changed") {
		t.Fatal(rec.Code, rec.Body)
	}
	rec = f.do("POST", "/api/ssh/forget", jsonBody(map[string]any{"host": host, "port": port}))
	if rec.Code != 204 {
		t.Fatal(rec.Code, rec.Body)
	}
	delete(body, "acceptHostKey")
	rec = f.do("POST", "/api/ssh/connect", jsonBody(body))
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), "ssh-host-unknown") {
		t.Fatal(rec.Body)
	}
	// Extra credential fields are rejected at the settings boundary.
	if rec = f.do("POST", "/api/settings", `{"password":"api-memory-only"}`); rec.Code != 400 {
		t.Fatal("settings accepted a password")
	}
	var saved Settings
	if json.Unmarshal(data, &saved) != nil || len(saved.RecentHosts) != 1 || saved.RecentHosts[0] != (RecentHost{Host: host, Port: port, User: "riko", Mode: "network"}) {
		t.Fatal("recent host missing")
	}
}

func TestSSHConnectionHasNoModeChoice(t *testing.T) {
	f := newFixture(t)
	rec := f.do("POST", "/api/ssh/connect", `{"mode":"other","host":"fake.invalid","user":"riko","password":"never-log-this"}`)
	if rec.Code != 400 || errorCode(t, rec.Body.Bytes()) != "bad_request" || strings.Contains(rec.Body.String(), "never-log-this") {
		t.Fatal(rec.Code, rec.Body)
	}
}

type failingSSHDialer struct{ err error }

func (d failingSSHDialer) DialContext(context.Context, string, string) (net.Conn, error) {
	return nil, d.err
}

func TestSSHConnectionErrorCodes(t *testing.T) {
	f, svc, _, _, _ := installFixture(t, true)
	dir := t.TempDir()
	svc.ConfigureStorage(dir, filepath.Join(dir, "cache"), filepath.Join(dir, "backups"), &guiCatalog{})
	for _, tc := range []struct {
		code string
		err  error
	}{
		{"ssh-refused", syscall.ECONNREFUSED},
		{"ssh-timeout", context.DeadlineExceeded},
		{"ssh-no-greeting", io.EOF},
	} {
		svc.SetSSHDialer(failingSSHDialer{tc.err})
		rec := f.do("POST", "/api/ssh/connect", `{"host":"fake.invalid","user":"riko","password":"never-log-this"}`)
		if rec.Code != 409 || errorCode(t, rec.Body.Bytes()) != tc.code || strings.Contains(rec.Body.String(), "never-log-this") {
			t.Fatalf("%s: %d %s", tc.code, rec.Code, rec.Body)
		}
	}
	board := testssh.NewN1(t, "correct-password")
	host, port := board.Server.HostPort()
	body := map[string]any{"host": host, "port": port, "user": "riko", "password": "wrong-password", "acceptHostKey": board.Server.Fingerprint()}
	svc.SetSSHDialer(nil)
	rec := f.do("POST", "/api/ssh/connect", jsonBody(body))
	if rec.Code != 409 || errorCode(t, rec.Body.Bytes()) != "ssh-authentication" {
		t.Fatal(rec.Code, rec.Body)
	}
}

type sshKeyNative struct {
	*fakeNative
	file  string
	calls int
}

func (n *sshKeyNative) OpenSSHKey() (string, error) { n.calls++; return n.file, nil }

func TestSSHPrivateKeyNativePickerAndBrowserFallback(t *testing.T) {
	f := newFixture(t)
	n := &sshKeyNative{fakeNative: f.native, file: "/fake/private/id_ed25519"}
	for _, web := range []bool{false, true} {
		f.handler = Handler(Deps{Service: f.service, Hub: f.hub, Settings: f.settings, Native: n, Web: web})
		rec := f.do("POST", "/api/ssh/key/pick", `{}`)
		if !web && (rec.Code != 200 || !strings.Contains(rec.Body.String(), n.file)) {
			t.Fatal(rec.Code, rec.Body)
		}
		if web && (rec.Code != 409 || errorCode(t, rec.Body.Bytes()) != "no-dialogs") {
			t.Fatal(rec.Code, rec.Body)
		}
	}
	if n.calls != 1 {
		t.Fatal("browser mode opened a native dialog")
	}
}
