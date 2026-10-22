// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package cli

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/app"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/testssh"
)

func TestSSHFlagsAndTargets(t *testing.T) {
	for _, target := range []string{"root@box:2222", "box", "user@[::1]:22", "[::1]"} {
		if host, _, port, err := parseSSHTarget(target); err != nil || host == "" || port < 1 {
			t.Fatalf("%s: %v", target, err)
		}
	}
	for _, target := range []string{"", "host:0", "host:65536", "host:not-port", "https://host"} {
		if _, _, _, err := parseSSHTarget(target); err == nil {
			t.Fatalf("accepted %s", target)
		}
	}
	var f sshFlags
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	f.register(fs)
	if err := fs.Parse(interspersed([]string{"--ssh", "root@box:2222", "--ssh-key", "key", "--accept-host-key", "SHA256:abc"})); err != nil || !f.enabled() || f.target != "root@box:2222" || f.key != "key" || f.accept != "SHA256:abc" {
		t.Fatal(f, err)
	}
	var names []string
	fs.VisitAll(func(f *flag.Flag) { names = append(names, f.Name) })
	if !reflect.DeepEqual(names, []string{"accept-host-key", "ssh", "ssh-key"}) {
		t.Fatal("unexpected SSH flags", names)
	}
	for _, f := range []sshFlags{{key: "key"}, {accept: "SHA256:abc"}} {
		if err := f.connect(context.Background(), nil, nil, io.Discard); err == nil || err.Error() != "Use --ssh / 请使用 --ssh" {
			t.Fatal("missing network target accepted", err)
		}
	}
}
func TestCLISSHDevicesConfigAndTOFU(t *testing.T) {
	const password = "cli-in-memory-secret"
	t.Setenv("OXP_FLASHER_SSH_PASSWORD", password)
	_, _, _ = engineFixture(t)
	fakeService := commandService
	commandService = func(hardware bool, events app.Events) (*app.Service, func(), error) {
		if hardware {
			t.Fatal("SSH command requested hardware access")
		}
		return fakeService(hardware, events)
	}
	board := testssh.NewN1(t, password)
	host, port := board.Server.HostPort()
	flags := []string{"--ssh", fmt.Sprintf("riko@%s:%d", host, port), "--json"}
	var out, errout bytes.Buffer
	code := runEngineCLI("devices", flags, nil, &out, &errout)
	if code != 1 || !strings.Contains(out.String()+errout.String(), board.Server.Fingerprint()) {
		t.Fatal(code, out.String(), errout.String())
	}
	flags = append(flags, "--accept-host-key", board.Server.Fingerprint())
	out.Reset()
	errout.Reset()
	if code = runEngineCLI("devices", flags, nil, &out, &errout); code != 0 || !strings.Contains(out.String(), "phicomm-n1") {
		t.Fatal(code, out.String(), errout.String())
	}
	if strings.Contains(out.String()+errout.String(), password) {
		t.Fatal("secret in CLI output")
	}
	out.Reset()
	errout.Reset()
	if code = runEngineCLI("config-get", append([]string{"/var/lib/openxplay/settings.json"}, flags...), nil, &out, &errout); code != 0 || out.String() != "{}" {
		t.Fatal(code, out.String(), errout.String())
	}
	// Flags are accepted by both planning and installation without making external requests.
	for _, cmd := range []string{"plan", "install"} {
		out.Reset()
		errout.Reset()
		code = runEngineCLI(cmd, append(flags, "--choice", "relay", "--yes"), nil, &out, &errout)
		if code == 2 || strings.Contains(out.String()+errout.String(), "flag provided but not defined") {
			t.Fatal(cmd, code, errout.String())
		}
	}
}
