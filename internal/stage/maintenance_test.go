// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-22

package stage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/adb"
)

func TestPackageMaintenanceBusyThenFree(t *testing.T) {
	e, dev, plan := setupExecutor(t, "full")
	e.MaintenanceInterval = time.Millisecond
	probes := 0
	dev.hook = func(_ context.Context, cmd string) (adb.Result, error, bool) {
		if cmd != packageMaintenanceProbe {
			return adb.Result{}, nil, false
		}
		probes++
		out := "free\n"
		if probes <= 2 {
			out = "busy\n"
		}
		return adb.Result{Stdout: []byte(out)}, nil, true
	}
	progress := false
	e.Emit = func(j Job) { progress = progress || j.Progress == PackageMaintenanceMessage }
	job := e.Run(t.Context(), plan)
	if job.Status != "succeeded" || probes < 5 || !progress || job.Progress != "" || !strings.Contains(job.Log, PackageMaintenanceMessage) {
		t.Fatalf("status=%s problem=%v probes=%d progress=%t", job.Status, job.Problem, probes, progress)
	}
	for i, cmd := range dev.commands {
		if strings.HasPrefix(cmd, "date -u -s") && dev.commands[i+1] != packageMaintenanceProbe {
			t.Fatal("clock was not followed by a maintenance probe")
		}
		if strings.Contains(cmd, "sh install-device.sh .") || strings.Contains(cmd, "--dry-run .") || strings.Contains(cmd, "openxplay-relay-update --sha256") {
			if dev.commands[i-1] != packageMaintenanceProbe {
				t.Fatal("package command was not preceded by a maintenance probe")
			}
		}
	}
}

func TestPackageMaintenanceTimeoutAndProbeErrors(t *testing.T) {
	for _, tc := range []struct {
		name, code string
		result     adb.Result
	}{
		{"busy", "package-maintenance-timeout", adb.Result{Stdout: []byte("busy\n")}},
		{"bad response", "package-maintenance-probe", adb.Result{Stdout: []byte("unrecognized\n")}},
		{"failed probe", "package-maintenance-probe", adb.Result{ExitCode: 2, Stderr: []byte("cannot read /proc/locks")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, dev, plan := setupExecutor(t, "relay")
			e.MaintenanceTimeout, e.MaintenanceInterval = 15*time.Millisecond, time.Millisecond
			dev.hook = func(_ context.Context, cmd string) (adb.Result, error, bool) {
				return tc.result, nil, cmd == packageMaintenanceProbe
			}
			job := e.Run(t.Context(), plan)
			if job.Status != "failed" || job.Problem.Code != tc.code || job.Steps[job.Failed].Action != "clock" || dev.updates != 0 || dev.firstInstalls != 0 {
				t.Fatalf("job=%s problem=%v", job.Status, job.Problem)
			}
			if tc.name == "busy" && !strings.Contains(job.Problem.Detail, "dpkg") || tc.name == "failed probe" && !strings.Contains(job.Problem.Detail, "cannot read /proc/locks") {
				t.Fatal("unclear maintenance error", job.Problem)
			}
		})
	}
}

func TestInstallLockWaitAndOneRetry(t *testing.T) {
	for _, kind := range []string{"device", "relay-update", "relay-first", "rolled-back lock", "still locked", "other failure", "wait timeout"} {
		t.Run(kind, func(t *testing.T) {
			choice := "relay"
			if kind == "device" {
				choice = "device"
			}
			e, dev, plan := setupExecutor(t, choice)
			e.MaintenanceInterval = time.Millisecond
			if kind == "relay-first" {
				delete(dev.installed, "openxplay-relay")
			}
			attempts, busy := 0, 0
			dev.hook = func(_ context.Context, cmd string) (adb.Result, error, bool) {
				if cmd == packageMaintenanceProbe && busy > 0 {
					busy--
					return adb.Result{Stdout: []byte("busy\n")}, nil, true
				}
				install := strings.Contains(cmd, "sh install-device.sh .") || strings.Contains(cmd, "openxplay-relay-update --sha256") || strings.Contains(cmd, "dpkg -i ")
				if !install {
					return adb.Result{}, nil, false
				}
				attempts++
				if attempts == 1 || kind == "still locked" {
					busy = 2
					message := "dpkg: error: dpkg frontend lock was locked by another process with pid 123\nOXP-STAGE relay failed: dpkg failed\n"
					if kind == "other failure" {
						message = "OXP-STAGE relay failed: disk full\n"
					}
					if kind == "wait timeout" {
						busy = 1000
						e.MaintenanceTimeout = 10 * time.Millisecond
					}
					if kind == "rolled-back lock" {
						return adb.Result{ExitCode: 1, Stderr: []byte(message), Stdout: []byte("OXP-STAGE relay rolled-back previous=0.1.3\n")}, nil, true
					}
					return adb.Result{ExitCode: 1, Stderr: []byte(message)}, nil, true
				}
				return adb.Result{}, nil, false
			}
			job := e.Run(t.Context(), plan)
			switch kind {
			case "still locked":
				if job.Status != "failed" || attempts != 2 || !strings.Contains(job.Problem.Detail, "dpkg frontend lock") {
					t.Fatalf("attempts=%d problem=%v", attempts, job.Problem)
				}
			case "other failure":
				if job.Status != "failed" || attempts != 1 || !strings.Contains(job.Problem.Detail, "disk full") {
					t.Fatalf("attempts=%d problem=%v", attempts, job.Problem)
				}
			case "wait timeout":
				if job.Status != "failed" || attempts != 1 || job.Problem.Code != "package-maintenance-timeout" {
					t.Fatalf("attempts=%d problem=%v", attempts, job.Problem)
				}
			default:
				if job.Status != "succeeded" || attempts != 2 || !strings.Contains(job.Log, PackageMaintenanceMessage) {
					t.Fatalf("attempts=%d status=%s problem=%v", attempts, job.Status, job.Problem)
				}
			}
		})
	}
}

func TestFailedStageOutputInJobError(t *testing.T) {
	for _, stream := range []string{"stdout", "stderr", "last lines", "transport"} {
		t.Run(stream, func(t *testing.T) {
			e, dev, plan := setupExecutor(t, "relay")
			message := "OXP-STAGE relay failed: dpkg returned 1"
			if stream == "last lines" {
				message = "dpkg: cannot unpack package: no space left on device"
			}
			dev.hook = func(_ context.Context, cmd string) (adb.Result, error, bool) {
				if !strings.Contains(cmd, "openxplay-relay-update --sha256") {
					return adb.Result{}, nil, false
				}
				r := adb.Result{ExitCode: 1}
				if stream == "stdout" {
					r.Stdout = []byte(message + "\n" + strings.Repeat("trailing diagnostic\n", 20))
				} else {
					r.Stderr = []byte(message + "\n")
				}
				var err error
				if stream == "transport" {
					err = errors.New("connection lost")
				}
				return r, err, true
			}
			job := e.Run(t.Context(), plan)
			if job.Status != "failed" || job.Problem.Code != "device" || !strings.Contains(job.Problem.Detail, message) {
				t.Fatalf("missing device error: %v", job.Problem)
			}
		})
	}
}

func TestDPKGLockErrorRecognition(t *testing.T) {
	for _, text := range []string{
		"dpkg: error: dpkg frontend lock was locked by another process with pid 123",
		"dpkg: error: dpkg status database is locked by another process",
		"dpkg: error: status database area is locked by another process",
		"dpkg: error: failed to lock dpkg database directory: Resource temporarily unavailable",
		"E: Could not get lock /var/lib/dpkg/lock-frontend. It is held by process 123",
		"E: Unable to acquire the dpkg frontend lock (/var/lib/dpkg/lock-frontend), is another process using it?",
	} {
		if !dpkgLockError(text) {
			t.Error("missed lock error", text)
		}
	}
	for _, text := range []string{"dpkg: package is corrupt", "Could not open lock file /var/lib/dpkg/lock-frontend: Permission denied", "device: Resource temporarily unavailable"} {
		if dpkgLockError(text) {
			t.Error("retried a non-lock error", text)
		}
	}
}

func maintenanceProbeFixture(t *testing.T) (string, string, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the device probe uses a POSIX shell")
	}
	dir := t.TempDir()
	for _, name := range []string{"lock", "lock-frontend"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	putScript := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	putScript("systemctl", "printf '%s\\n' \"${PROBE_STATES-inactive}\"; exit \"${PROBE_RC-3}\"\n")
	probe := strings.NewReplacer("/var/lib/dpkg/lock", filepath.Join(dir, "lock")).Replace(packageMaintenanceProbe)
	return dir, probe, os.Getenv("PATH")
}

func TestPackageMaintenanceShellProbe(t *testing.T) {
	dir, probe, path := maintenanceProbeFixture(t)
	locks := filepath.Join(dir, "proc-locks")
	probe = strings.ReplaceAll(probe, "/proc/locks", locks)
	// Linux's dev_t encoding: major 0x103, minor 0x209. The lock-table key is 103:209:42.
	dev := uint64(0x103)<<8 | uint64(0x209&0xff) | uint64(0x209&^0xff)<<12
	stat := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' '%d 42'\n", dev)
	if err := os.WriteFile(filepath.Join(dir, "stat"), []byte(stat), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, states, locks, want string
		rc, wantExit              int
	}{
		{"idle with leftover files", "inactive\nfailed\nunknown", "", "free", 3, 0},
		{"apt daily", "active\ninactive\ninactive", "", "busy", 0, 0},
		{"apt upgrade", "inactive\nactive\ninactive", "", "busy", 0, 0},
		{"unattended", "inactive\ninactive\nactive", "", "busy", 0, 0},
		{"starting", "inactive\nactivating\ninactive", "", "busy", 3, 0},
		{"dpkg POSIX lock", "inactive", "1: POSIX  ADVISORY  WRITE 123 103:209:42 0 EOF\n", "busy", 3, 0},
		{"different filesystem", "inactive", "1: POSIX  ADVISORY  WRITE 123 01:01:42 0 EOF\n", "free", 3, 0},
		{"different inode", "inactive", "1: POSIX  ADVISORY  WRITE 123 103:209:43 0 EOF\n", "free", 3, 0},
		{"systemd failed", "Failed to connect to bus", "", "", 1, 2},
		{"unknown service state", "garbage", "", "", 3, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(locks, []byte(tc.locks), 0o600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.CommandContext(t.Context(), "sh", "-c", probe)
			cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+path, "PROBE_STATES="+tc.states, fmt.Sprintf("PROBE_RC=%d", tc.rc))
			out, err := cmd.Output()
			exit := 0
			if err != nil {
				var failed *exec.ExitError
				if !errors.As(err, &failed) {
					t.Fatal(err)
				}
				exit = failed.ExitCode()
			}
			if exit != tc.wantExit || strings.TrimSpace(string(out)) != tc.want {
				t.Fatalf("exit=%d out=%q error=%v", exit, out, err)
			}
		})
	}
}
