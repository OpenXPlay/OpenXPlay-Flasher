// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package stage

import (
	"context"
	"fmt"
	"maps"
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
	if err := readPackages(ctx, run, names, f.Packages); err != nil {
		return f, err
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
	if err := readSpace(ctx, run, dirs, f.FreeKiB); err != nil {
		return f, err
	}
	return f, nil
}

// InspectPackageRequirements augments a successful profile-only observation. The service records
// an error against this package's need, so one unavailable package requirement cannot gate Choose
// or an unrelated layer. The executor still uses Inspect for all of its own final checks.
func InspectPackageRequirements(ctx context.Context, run Run, f Facts, pkg *Package) (Facts, error) {
	f.Packages, f.FreeKiB = maps.Clone(f.Packages), maps.Clone(f.FreeKiB)
	if f.Packages == nil {
		f.Packages = map[string]string{}
	}
	if f.FreeKiB == nil {
		f.FreeKiB = map[string]int64{}
	}
	if err := readPackages(ctx, run, slices.Clone(pkg.manifest.Requires.Packages), f.Packages); err != nil {
		return f, err
	}
	if err := readSpace(ctx, run, []string{pkg.manifest.DeviceDir}, f.FreeKiB); err != nil {
		return f, err
	}
	return f, nil
}

func readPackages(ctx context.Context, run Run, names []string, versions map[string]string) error {
	slices.Sort(names)
	names = slices.Compact(names)
	for _, name := range names {
		if !debName.MatchString(name) {
			return fmt.Errorf("invalid package name: %s", name)
		}
		cmd := "dpkg-query -W -f '${db:Status-Status}\\t${Version}' " + quote(name)
		r, err := run(ctx, cmd)
		if err != nil {
			return err
		}
		switch r.ExitCode {
		case 0:
			status, version, ok := strings.Cut(strings.TrimRight(string(r.Stdout), "\r\n"), "\t")
			if !ok {
				return fmt.Errorf("invalid dpkg-query response for %s", name)
			}
			if status == "installed" {
				if !validVersion(version) {
					return fmt.Errorf("invalid installed version for %s", name)
				}
				versions[name] = version
			}
		case 1:
			// dpkg-query returns 1 when a package is not installed.
		default:
			return fmt.Errorf("dpkg-query %s: exit %d", name, r.ExitCode)
		}
	}
	return nil
}

func readSpace(ctx context.Context, run Run, dirs []string, free map[string]int64) error {
	slices.Sort(dirs)
	for _, dir := range slices.Compact(dirs) {
		cmd := "d=" + quote(dir) + `; while [ ! -d "$d" ]; do d=${d%/*}; [ -n "$d" ] || d=/; done; df -Pk "$d"`
		r, err := run(ctx, cmd)
		if err != nil {
			return err
		}
		lines := strings.Split(strings.TrimSpace(string(r.Stdout)), "\n")
		parts := strings.Fields(lines[len(lines)-1])
		if r.ExitCode != 0 || len(parts) < 4 {
			return fmt.Errorf("cannot read free space for %s", dir)
		}
		space, err := strconv.ParseInt(parts[3], 10, 64)
		if err != nil || space < 0 {
			return fmt.Errorf("invalid free space for %s", dir)
		}
		free[dir] = space
	}
	return nil
}
