// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package stage

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/adb"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/profile"
)

type Facts struct {
	Root             bool              `json:"root"`
	KernelRelease    string            `json:"kernelRelease"`
	DeviceTreeModel  string            `json:"deviceTreeModel"`
	DebianVersion    string            `json:"debianVersion"`
	DPKGArchitecture string            `json:"dpkgArchitecture"`
	Layers           map[string]string `json:"layers"`
	Packages         map[string]string `json:"packages"`
	FreeKiB          map[string]int64  `json:"freeKiB"`
}

// Run is the shell boundary shared by inspection and the executor. The executor adds timeouts,
// write guards and logging around it.
type Run func(context.Context, string) (adb.Result, error)

func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

const factsCommand = `set -eu
printf 'root\t'; id -u
printf 'kernel\t'; uname -r
printf 'model\t'; tr -d '\000' < /proc/device-tree/model; printf '\n'
printf 'debian\t'; (. /etc/os-release; printf '%s\n' "$VERSION_ID")
printf 'arch\t'; dpkg --print-architecture`

// Inspect reads facts without creating folders or changing the device. Space is measured on the
// nearest existing parent of each package's cache directory.
func Inspect(ctx context.Context, run Run, p profile.Profile, packages []*Package) (Facts, error) {
	f := Facts{Layers: map[string]string{}, Packages: map[string]string{}, FreeKiB: map[string]int64{}}
	res, err := run(ctx, factsCommand)
	if err != nil {
		return f, err
	}
	if res.ExitCode != 0 {
		return f, fmt.Errorf("read device facts: exit %d", res.ExitCode)
	}
	fields := map[string]string{}
	for _, line := range strings.Split(strings.ReplaceAll(string(res.Stdout), "\r\n", "\n"), "\n") {
		key, val, ok := strings.Cut(line, "\t")
		if ok {
			fields[key] = val
		}
	}
	for _, key := range []string{"root", "kernel", "model", "debian", "arch"} {
		if fields[key] == "" {
			return f, fmt.Errorf("missing device fact: %s", key)
		}
	}
	f.Root, f.KernelRelease, f.DeviceTreeModel = fields["root"] == "0", fields["kernel"], fields["model"]
	f.DebianVersion, f.DPKGArchitecture = fields["debian"], fields["arch"]
	names, dirs := []string{}, []string{"/var/cache"}
	for _, l := range p.Layers {
		if l.Package != "" {
			names = append(names, l.Package)
		}
	}
	for _, pkg := range packages {
		names = append(names, pkg.manifest.Requires.Packages...)
		dirs = append(dirs, pkg.manifest.DeviceDir)
	}
	slices.Sort(names)
	names = slices.Compact(names)
	for _, name := range names {
		if !debName.MatchString(name) {
			return f, fmt.Errorf("invalid package name: %s", name)
		}
		cmd := "dpkg-query -W -f '${db:Status-Status}\\t${Version}' " + quote(name)
		r, err := run(ctx, cmd)
		if err != nil {
			return f, err
		}
		switch r.ExitCode {
		case 0:
			status, version, ok := strings.Cut(strings.TrimRight(string(r.Stdout), "\r\n"), "\t")
			if !ok {
				return f, fmt.Errorf("invalid dpkg-query response for %s", name)
			}
			if status == "installed" {
				if !validVersion(version) {
					return f, fmt.Errorf("invalid installed version for %s", name)
				}
				f.Packages[name] = version
			}
		case 1:
			// dpkg-query returns 1 when a package is not installed.
		default:
			return f, fmt.Errorf("dpkg-query %s: exit %d", name, r.ExitCode)
		}
	}
	for _, l := range p.Layers {
		f.Layers[l.ID] = f.Packages[l.Package]
	}
	base, _ := p.Layer("base")
	kernel, model := base.KernelRelease, base.DeviceTreeModel
	for _, pkg := range packages {
		if pkg.manifest.Stage == "device" && pkg.manifest.Device == p.ID {
			kernel, model = pkg.manifest.Requires.KernelRelease, pkg.manifest.Requires.DeviceTreeModel
		}
	}
	if kernel != "" && model != "" && f.KernelRelease == kernel && f.DeviceTreeModel == model {
		f.Layers["base"] = f.KernelRelease
	}
	slices.Sort(dirs)
	for _, dir := range slices.Compact(dirs) {
		cmd := "d=" + quote(dir) + `; while [ ! -d "$d" ]; do d=${d%/*}; [ -n "$d" ] || d=/; done; df -Pk "$d"`
		r, err := run(ctx, cmd)
		if err != nil {
			return f, err
		}
		lines := strings.Split(strings.TrimSpace(string(r.Stdout)), "\n")
		parts := strings.Fields(lines[len(lines)-1])
		if r.ExitCode != 0 || len(parts) < 4 {
			return f, fmt.Errorf("cannot read free space for %s", dir)
		}
		space, err := strconv.ParseInt(parts[3], 10, 64)
		if err != nil || space < 0 {
			return f, fmt.Errorf("invalid free space for %s", dir)
		}
		f.FreeKiB[dir] = space
	}
	return f, nil
}
