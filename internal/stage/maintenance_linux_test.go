// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-22

package stage

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// Exercise the actual Linux kernel lock table and Debian's stat/awk. Only disposable files
// are locked; systemd service responses are faked and no package manager is invoked.
func TestPackageMaintenanceProbeRecordLocks(t *testing.T) {
	dir, probe, path := maintenanceProbeFixture(t)
	check := func(want string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "sh", "-c", probe)
		cmd.Env = append(os.Environ(), "PATH="+dir+":"+path)
		out, err := cmd.CombinedOutput()
		if err != nil || strings.TrimSpace(string(out)) != want {
			t.Fatalf("probe=%q error=%v; want %s", out, err, want)
		}
	}
	check("free")
	for _, name := range []string{"lock", "lock-frontend"} {
		f, err := os.OpenFile(filepath.Join(dir, name), os.O_RDWR, 0)
		if err != nil {
			t.Fatal(err)
		}
		lock := syscall.Flock_t{Type: syscall.F_WRLCK, Whence: 0, Start: 0, Len: 0}
		if err := syscall.FcntlFlock(f.Fd(), syscall.F_SETLK, &lock); err != nil {
			f.Close()
			t.Fatal(err)
		}
		check("busy")
		f.Close()
		check("free")
	}
}
