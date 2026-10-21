// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package stage

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/adb"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/profile"
	sshdev "github.com/OpenXPlay/OpenXPlay-Flasher/internal/ssh"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/testssh"
)

func TestN1LayersOverSSHAndReboot(t *testing.T) {
	profiles, _ := profile.Builtin()
	var p profile.Profile
	for _, v := range profiles {
		if v.ID == "phicomm-n1" {
			p = v
		}
	}
	dm := deviceManifest()
	dm.Device = "phicomm-n1"
	dm.Name = "openxplay-phicomm-n1-device-0.1.0"
	dm.Transport = "ssh"
	dm.Requires.KernelRelease = ""
	dm.Requires.DeviceTreeModel = "Phicomm N1"
	dm.Requires.DebianVersion = "13"
	dm.Packages[2].Name = "openxplay-phicomm-n1"
	dm.Packages[2].File = "debs/openxplay-phicomm-n1.deb"
	dm.Packages[2].SHA256 = digest([]byte("openxplay-phicomm-n1"))
	device, err := DevicePackage(bundleBytes(t, dm, nil))
	if err != nil {
		t.Fatal(err)
	}
	rm := relayManifest()
	rm.Transport = "ssh"
	rm.Requires.DebianVersion = "13"
	relay, err := RelayPackage(jsonBytes(t, rm), []byte("relay deb"))
	if err != nil {
		t.Fatal(err)
	}
	scripted := newScriptDevice(t, device, relay)
	var mu sync.Mutex
	scripted.hook = func(ctx context.Context, cmd string) (adb.Result, error, bool) {
		if cmd == factsCommand {
			return adb.Result{Stdout: []byte("root\t0\nkernel\t6.12.999-ophub\nmodel\tPhicomm N1\ndebian\t13\narch\tarm64\npretty\tDebian GNU/Linux 13 (trixie)\n")}, nil, true
		}
		if strings.HasSuffix(cmd, quote("openxplay-phicomm-n1")) {
			v := scripted.installed["openxplay-phicomm-n1"]
			if v == "" {
				return adb.Result{ExitCode: 1}, nil, true
			}
			return adb.Result{Stdout: []byte("installed\t" + v)}, nil, true
		}
		return adb.Result{}, nil, false
	}
	const password = "stage-secret-never-log"
	s := testssh.New(t, password, nil, func(cmd string, in io.Reader, out, stderr io.Writer) int {
		mu.Lock()
		defer mu.Unlock()
		command := testssh.Unwrap(cmd)
		if strings.Contains(command, "mktemp") {
			for _, line := range strings.Split(command, "\n") {
				if strings.HasPrefix(line, "target=") {
					name := testssh.Unwrap("sh -c " + strings.TrimPrefix(line, "target="))
					data, _ := io.ReadAll(in)
					scripted.files[name] = data
					break
				}
			}
			return 0
		}
		timeout := 10 * time.Minute
		if strings.Contains(command, "--dry-run") {
			timeout = 2 * time.Minute
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		r, err := scripted.Shell(ctx, command)
		if err != nil {
			return 1
		}
		_, _ = out.Write(r.Stdout)
		_, _ = stderr.Write(r.Stderr)
		return r.ExitCode
	})
	host, port := s.HostPort()
	connector, err := sshdev.NewConnector(sshdev.Options{Host: host, Port: port, User: "root", Password: password, KnownHosts: filepath.Join(t.TempDir(), "known_hosts"), AcceptHostKey: s.Fingerprint()})
	if err != nil {
		t.Fatal(err)
	}
	d, err := connector.Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	installed, err := Inspect(context.Background(), d.Shell, p, []*Package{device, relay})
	d.Close()
	if err != nil {
		t.Fatal(err)
	}
	if installed.Layers["base"] != "6.12.999-ophub" {
		t.Fatalf("kernel unexpectedly pinned: %+v", installed)
	}
	plan := BuildPlan(p, installed, []*Package{device, relay}, Request{Choice: "full"})
	if plan.Problem != nil {
		t.Fatal(plan.Problem)
	}
	e := NewExecutor(p, []*Package{device, relay})
	e.Connect = connector.Connect
	e.Guard = func(context.Context) error { return nil }
	e.PollInterval = time.Millisecond
	e.RebootTimeout = time.Second
	job := e.Run(context.Background(), plan)
	if job.Status != "succeeded" || job.Installed.Layers["device"] != "0.1.0" || job.Installed.Layers["relay"] != rm.Version {
		t.Fatalf("%+v\n%s", job, job.Log)
	}
	if strings.Contains(job.Log, password) {
		t.Fatal("secret in stage log")
	}
	if !strings.Contains(job.Log, "Device returned after reboot") {
		t.Fatal("missing reconnect")
	}
	// The same generic relay payload can be used by multiple profiles when it declares no
	// device-specific OS restriction, including the original sidecar's adb transport spelling.
	generic := rm
	generic.Transport = "adb"
	generic.Requires.DebianVersion = ""
	payload, err := RelayPackage(jsonBytes(t, generic), []byte("relay deb"))
	if err != nil {
		t.Fatal(err)
	}
	if problem := CheckRequirements(*job.Installed, payload); problem != nil {
		t.Fatal(fmt.Sprint(problem))
	}
}
