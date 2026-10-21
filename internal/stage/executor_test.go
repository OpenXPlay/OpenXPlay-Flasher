// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package stage

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"testing"
	"time"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/adb"
)

// scriptDevice is an adb.Device with scripted shell results and an in-memory package database.
// It never opens a USB bus, an adb server or a process on the host.
type scriptDevice struct {
	installed              map[string]string
	files                  map[string][]byte
	commands               []string
	boot                   string
	noBoot                 bool
	verifyCalls            int
	verifyFailures         int
	firstInstalls, updates int
	closed                 int
	t                      *testing.T
	device, relay          *Package
	hook                   func(context.Context, string) (adb.Result, error, bool)
}

func newScriptDevice(t *testing.T, d, r *Package) *scriptDevice {
	return &scriptDevice{t: t, device: d, relay: r, boot: "boot-before", files: map[string][]byte{},
		installed: map[string]string{"bluez": "5.55", "iw": "5.9", "network-manager": "1.30", "openxplay-ufi003": "0.1.0", "openxplay-relay": "0.1.3"}}
}

func (d *scriptDevice) Shell(ctx context.Context, cmd string) (adb.Result, error) {
	d.commands = append(d.commands, cmd)
	if d.hook != nil {
		if r, err, handled := d.hook(ctx, cmd); handled {
			return r, err
		}
	}
	out := ""
	switch {
	case cmd == factsCommand:
		out = "root\t0\nkernel\t5.15.0-handsomekernel+\nmodel\tHandsome Openstick\ndebian\t11\narch\tarm64\n"
	case strings.HasPrefix(cmd, "dpkg-query "):
		for _, name := range []string{"bluez", "iw", "network-manager", "openxplay-ufi003", "openxplay-relay"} {
			if strings.HasSuffix(cmd, quote(name)) {
				if d.installed[name] == "" {
					return adb.Result{ExitCode: 1}, nil
				}
				return adb.Result{Stdout: []byte("installed\t" + d.installed[name])}, nil
			}
		}
		return adb.Result{ExitCode: 1}, nil
	case strings.Contains(cmd, "df -Pk"):
		out = "Filesystem 1024-blocks Used Available Capacity Mounted\n/dev/root 1000000 200000 800000 20% /\n"
	case strings.HasPrefix(cmd, "sha256sum "):
		found := false
		for name, data := range d.files {
			if cmd == "sha256sum "+quote(name) {
				out = digest(data) + "  " + name + "\n"
				found = true
			}
		}
		if !found {
			return adb.Result{ExitCode: 1}, nil
		}
	case strings.Contains(cmd, "--dry-run ."):
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 2*time.Minute {
			d.t.Error("preflight does not have a two-minute timeout")
		}
		out = "preflight output\nOXP-STAGE device preflight-ok version=0.1.0\n"
	case strings.Contains(cmd, "--verify ."):
		d.verifyCalls++
		if d.verifyCalls <= d.verifyFailures {
			return adb.Result{ExitCode: 1, Stdout: []byte("Wi-Fi power save off: failed\n")}, nil
		}
		out = "OXP-STAGE device verified version=0.1.0\n"
	case strings.Contains(cmd, "sh install-device.sh ."):
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) < 9*time.Minute {
			d.t.Error("install does not have a ten-minute timeout")
		}
		for _, p := range d.device.manifest.Packages {
			d.installed[p.Name] = p.Version
		}
		out = "OXP-STAGE device ok version=0.1.0 reboot=required\n"
	case strings.Contains(cmd, "dpkg -i "):
		d.firstInstalls++
		d.installed["openxplay-relay"] = d.relay.manifest.Version
		out = "Setting up openxplay-relay\nOXP-STAGE relay ok\n"
	case strings.Contains(cmd, "openxplay-relay-update --sha256"):
		d.updates++
		d.installed["openxplay-relay"] = d.relay.manifest.Version
		out = "OXP-STAGE relay ok version=" + d.relay.manifest.Version + "\n"
	case strings.Contains(cmd, "systemctl is-active"):
		out = "active\n"
	case cmd == "cat /proc/sys/kernel/random/boot_id":
		out = d.boot + "\n"
	case cmd == "systemctl --no-block reboot":
		if !d.noBoot {
			d.boot = "boot-after"
		}
	case strings.HasPrefix(cmd, "date -u -s @"), strings.HasPrefix(cmd, "umask 077; mkdir -p "):
	case strings.HasPrefix(cmd, "rm -rf ") && strings.Contains(cmd, "tar -xzmf ") && strings.Contains(cmd, "sha256sum -c SHA256SUMS"):
		out = "manifest.json: OK\n"
	default:
		d.t.Errorf("unscripted command: %s", cmd)
		return adb.Result{ExitCode: 127}, nil
	}
	return adb.Result{Stdout: []byte(out)}, nil
}

func (d *scriptDevice) Push(_ context.Context, name string, data []byte, mode fs.FileMode) error {
	if mode != 0o644 || !strings.HasPrefix(name, "/var/cache/openxplay/") {
		d.t.Errorf("unsafe push: %s mode=%o", name, mode)
	}
	d.files[name] = data
	return nil
}
func (d *scriptDevice) Pull(context.Context, string, int64) ([]byte, error) {
	return nil, errors.New("unexpected pull")
}
func (d *scriptDevice) Close() error { d.closed++; return nil }

func setupExecutor(t *testing.T, choice string) (*Executor, *scriptDevice, Plan) {
	t.Helper()
	d, r := checkedPackages(t)
	packages := []*Package{d, r}
	p := builtinProfile(t)
	e := NewExecutor(p, packages)
	dev := newScriptDevice(t, d, r)
	e.Connect = func(context.Context) (adb.Device, error) { return dev, nil }
	e.Guard = func(context.Context) error { return nil }
	e.RebootTimeout, e.VerifyTimeout, e.PollInterval, e.VerifyInterval = 20*time.Millisecond, 20*time.Millisecond, time.Millisecond, time.Millisecond
	plan := BuildPlan(p, installedFacts(), packages, Request{Choice: choice})
	if plan.Problem != nil {
		t.Fatal(plan.Problem)
	}
	return e, dev, plan
}

func TestExecutorFullInstall(t *testing.T) {
	e, dev, plan := setupExecutor(t, "full")
	dev.verifyFailures = 1
	guards := 0
	e.Guard = func(context.Context) error { guards++; return nil }
	var events []Job
	e.Emit = func(j Job) { events = append(events, j) }
	job := e.Run(context.Background(), plan)
	if job.Status != "succeeded" || !job.Observed || job.Installed.Layers["relay"] != "0.1.5~beta.1" || dev.verifyCalls != 2 {
		t.Fatalf("job=%+v, verify calls=%d", job, dev.verifyCalls)
	}
	if guards < 14 {
		t.Fatalf("too few write guards: %d", guards)
	}
	relayIndex, verifyIndex := -1, -1
	for i, cmd := range dev.commands {
		if strings.Contains(cmd, "openxplay-relay-update --sha256") {
			relayIndex = i
		}
		if strings.Contains(cmd, "--verify .") {
			verifyIndex = i
		}
	}
	if verifyIndex <= relayIndex {
		t.Fatal("device was verified before the relay")
	}
	if events[0].Status != "running" || events[len(events)-1].Status != "succeeded" || events[0].Steps[0].Status != "pending" {
		t.Fatal("progress snapshots were changed after publishing")
	}
	if !strings.Contains(job.Log, "preflight output") || !strings.Contains(job.Log, "adb returned after reboot") {
		t.Fatal("command output missing from log")
	}
	for _, s := range job.Steps {
		if s.Status != "ok" {
			t.Errorf("unfinished step: %+v", s)
		}
	}
}

func TestExecutorFirstRelayInstall(t *testing.T) {
	e, dev, plan := setupExecutor(t, "relay")
	delete(dev.installed, "openxplay-relay")
	job := e.Run(context.Background(), plan)
	if job.Status != "succeeded" || dev.firstInstalls != 1 || dev.updates != 0 {
		t.Fatalf("status=%s, first=%d, updates=%d, error=%v", job.Status, dev.firstInstalls, dev.updates, job.Problem)
	}
}

func TestExecutorRebootCommand(t *testing.T) {
	e, dev, _ := setupExecutor(t, "device")
	e.dev = dev
	if err := e.step(context.Background(), Step{Action: "reboot"}, dev.device); err != nil {
		t.Fatal(err)
	}
	want := []string{"cat /proc/sys/kernel/random/boot_id", "systemctl --no-block reboot"}
	if len(dev.commands) != len(want) {
		t.Fatalf("commands=%q, want %q", dev.commands, want)
	}
	for i, cmd := range want {
		if dev.commands[i] != cmd {
			t.Errorf("command %d=%q, want %q", i, dev.commands[i], cmd)
		}
	}
	if e.bootID != "boot-before" || dev.closed != 1 || e.dev != nil {
		t.Fatalf("boot ID=%q, closed=%d, connection=%v", e.bootID, dev.closed, e.dev)
	}
}

func TestExecutorRebootWaitsForNewBootID(t *testing.T) {
	e, dev, plan := setupExecutor(t, "full")
	e.RebootTimeout = time.Second
	dev.noBoot = true
	connects := 0
	e.Connect = func(context.Context) (adb.Device, error) {
		connects++
		switch connects {
		case 1, 2: // Initial connection, then the old boot still answering before shutdown.
			return dev, nil
		case 3:
			return nil, &Problem{Code: "no-device"}
		default:
			dev.boot = "boot-after"
			return dev, nil
		}
	}
	job := e.Run(context.Background(), plan)
	if job.Status != "succeeded" || connects != 4 {
		t.Fatalf("status=%s, problem=%v, connects=%d", job.Status, job.Problem, connects)
	}
}

func TestExecutorStopsOnFailure(t *testing.T) {
	for _, tc := range []struct {
		name, choice, code, action string
		setup                      func(*Executor, *scriptDevice)
	}{
		{"preflight failure", "full", "device", "preflight", func(e *Executor, d *scriptDevice) {
			d.hook = func(_ context.Context, cmd string) (adb.Result, error, bool) {
				if strings.Contains(cmd, "--dry-run .") {
					return adb.Result{ExitCode: 2, Stdout: []byte("preflight failed\n")}, nil, true
				}
				return adb.Result{}, nil, false
			}
		}},
		{"missing success line", "relay", "success-line", "install", func(e *Executor, d *scriptDevice) {
			d.hook = func(_ context.Context, cmd string) (adb.Result, error, bool) {
				if strings.Contains(cmd, "openxplay-relay-update --sha256") {
					return adb.Result{Stdout: []byte("done\n")}, nil, true
				}
				return adb.Result{}, nil, false
			}
		}},
		{"wrong last marker", "relay", "success-line", "install", func(e *Executor, d *scriptDevice) {
			d.hook = func(_ context.Context, cmd string) (adb.Result, error, bool) {
				if strings.Contains(cmd, "openxplay-relay-update --sha256") {
					return adb.Result{Stdout: []byte("OXP-STAGE relay ok\nOXP-STAGE relay failed\n")}, nil, true
				}
				return adb.Result{}, nil, false
			}
		}},
		{"rolled back", "relay", "rolled-back", "install", func(e *Executor, d *scriptDevice) {
			d.hook = func(_ context.Context, cmd string) (adb.Result, error, bool) {
				if strings.Contains(cmd, "openxplay-relay-update --sha256") {
					return adb.Result{ExitCode: 2, Stdout: []byte("OXP-STAGE relay rolled-back previous=0.1.3\n")}, nil, true
				}
				return adb.Result{}, nil, false
			}
		}},
		{"reboot not started", "full", "reboot-not-started", "wait", func(e *Executor, d *scriptDevice) { d.noBoot = true }},
		{"reboot timeout", "full", "reboot-timeout", "wait", func(e *Executor, d *scriptDevice) {
			e.Connect = func(context.Context) (adb.Device, error) {
				if d.closed > 0 {
					return nil, &Problem{Code: "no-device"}
				}
				return d, nil
			}
		}},
		{"old boot then disappears", "full", "reboot-timeout", "wait", func(e *Executor, d *scriptDevice) {
			d.noBoot = true
			connects := 0
			e.Connect = func(context.Context) (adb.Device, error) {
				connects++
				if connects > 2 {
					return nil, &Problem{Code: "no-device"}
				}
				return d, nil
			}
		}},
		{"device disappears", "full", "no-device", "push", func(e *Executor, d *scriptDevice) {
			n := 0
			e.Guard = func(context.Context) error {
				n++
				if n >= 4 {
					return &Problem{Code: "no-device"}
				}
				return nil
			}
		}},
		{"bad uploaded checksum", "relay", "device-checksum", "checksum", func(e *Executor, d *scriptDevice) {
			d.hook = func(_ context.Context, cmd string) (adb.Result, error, bool) {
				if strings.HasPrefix(cmd, "sha256sum ") {
					return adb.Result{Stdout: []byte(strings.Repeat("0", 64) + "  file\n")}, nil, true
				}
				return adb.Result{}, nil, false
			}
		}},
		{"partial install", "relay", "device", "install", func(e *Executor, d *scriptDevice) {
			d.hook = func(_ context.Context, cmd string) (adb.Result, error, bool) {
				if strings.Contains(cmd, "openxplay-relay-update --sha256") {
					d.installed["openxplay-relay"] = d.relay.manifest.Version
					return adb.Result{Stdout: []byte("connection lost after dpkg\n")}, errors.New("disconnected"), true
				}
				return adb.Result{}, nil, false
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, dev, plan := setupExecutor(t, tc.choice)
			tc.setup(e, dev)
			job := e.Run(context.Background(), plan)
			if job.Status != "failed" || job.Problem.Code != tc.code || job.Failed < 0 || job.Steps[job.Failed].Action != tc.action {
				t.Fatalf("status=%s problem=%v failed=%d steps=%v", job.Status, job.Problem, job.Failed, job.Steps)
			}
			for _, s := range job.Steps[job.Failed+1:] {
				if s.Status != "pending" {
					t.Errorf("ran after failure: %+v", s)
				}
			}
			if tc.code == "rolled-back" && (job.Left != "previous-relay" || job.Installed.Layers["relay"] != "0.1.3") {
				t.Fatal("lost rollback outcome")
			}
			if tc.name == "preflight failure" && (job.Left != "unchanged" || dev.updates != 0) {
				t.Fatal("installed after preflight failure")
			}
			if tc.name == "partial install" && (!job.Observed || job.Installed.Layers["relay"] != dev.relay.manifest.Version) {
				t.Fatal("did not read partial install state")
			}
		})
	}
}

func TestExecutorVerifyRetryDeadline(t *testing.T) {
	e, dev, plan := setupExecutor(t, "device")
	dev.verifyFailures = 1000
	job := e.Run(context.Background(), plan)
	if job.Status != "failed" || job.Steps[job.Failed].Action != "verify" || dev.verifyCalls < 2 {
		t.Fatalf("job=%s, calls=%d", job.Status, dev.verifyCalls)
	}
}

func TestRebootReconnectRejectsSeveralDevices(t *testing.T) {
	e, _, plan := setupExecutor(t, "full")
	original := e.Connect
	n := 0
	e.Connect = func(ctx context.Context) (adb.Device, error) {
		n++
		if n > 1 {
			return nil, &Problem{Code: "several-devices"}
		}
		return original(ctx)
	}
	job := e.Run(context.Background(), plan)
	if job.Problem == nil || job.Problem.Code != "several-devices" {
		t.Fatalf("job problem=%v", job.Problem)
	}
}

func TestSessionLogIsCapped(t *testing.T) {
	e := &Executor{}
	e.log(strings.Repeat("a", MaxLog))
	e.log("\nlast output\n")
	if len(e.job.Log) != MaxLog || !e.job.LogCapped || !strings.HasSuffix(e.job.Log, "last output\n") {
		t.Fatal("log cap lost the most recent output")
	}
}

func TestInspectBaseAndMissingLayers(t *testing.T) {
	d, r := checkedPackages(t)
	dev := newScriptDevice(t, d, r)
	delete(dev.installed, "openxplay-ufi003")
	delete(dev.installed, "openxplay-relay")
	f, err := Inspect(context.Background(), dev.Shell, builtinProfile(t), nil)
	if err != nil || f.Layers["base"] == "" || f.Layers["device"] != "" || f.Layers["relay"] != "" || f.FreeKiB["/var/cache"] == 0 {
		t.Fatalf("facts=%+v err=%v", f, err)
	}
	dev.hook = func(_ context.Context, cmd string) (adb.Result, error, bool) {
		if cmd == factsCommand {
			return adb.Result{Stdout: []byte("root\t0\nkernel\twrong\nmodel\tOther board\ndebian\t11\narch\tarm64\n")}, nil, true
		}
		return adb.Result{}, nil, false
	}
	f, err = Inspect(context.Background(), dev.Shell, builtinProfile(t), nil)
	if err != nil || f.Layers["base"] != "" {
		t.Fatalf("wrong base accepted: %v %v", f, err)
	}
}

func ExampleCompareVersions() {
	fmt.Println(CompareVersions("0.1.5~beta.1", "0.1.5")) // Output: -1
}
