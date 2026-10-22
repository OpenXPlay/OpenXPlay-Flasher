// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-22

package stage

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/adb"
)

const PackageMaintenanceMessage = "Waiting for the device's automatic package maintenance to finish… / 正在等待设备的自动软件包维护完成…"

// Debian's dpkg uses POSIX record locks, which flock cannot detect. The kernel lock table
// identifies each lock by the file's device and inode; a leftover lock file is not a held lock.
// These probes use systemd, GNU stat and POSIX awk, available in minimal Debian 11 and 13.
const packageMaintenanceProbe = `set -eu
states=$(systemctl is-active apt-daily.service apt-daily-upgrade.service unattended-upgrades.service 2>&1) || {
  rc=$?
  case "$rc" in 3|4) ;; *) printf '%s\n' "$states" >&2; exit 2 ;; esac
}
for state in $states; do
  case "$state" in
    active|activating|deactivating|reloading) printf 'busy\n'; exit 0 ;;
    inactive|failed|unknown) ;;
    *) printf 'Cannot read automatic package service state: %s\n' "$states" >&2; exit 2 ;;
  esac
done
[ -n "$states" ] || { printf 'Empty systemd service state\n' >&2; exit 2; }
for file in /var/lib/dpkg/lock /var/lib/dpkg/lock-frontend; do
  [ -e "$file" ] || continue
  numbers=$(stat -Lc '%d %i' "$file") || exit 2
  set -- $numbers
  dev=$1
  major=$(( ((dev >> 8) & 4095) | ((dev >> 32) & 4294963200) ))
  minor=$(( (dev & 255) | ((dev >> 12) & 4294967040) ))
  key=$(printf '%02x:%02x:%s' "$major" "$minor" "$2")
  held=$(awk -v key="$key" '$6 == key || $7 == key { held=1 } END { print held+0 }' /proc/locks) || exit 2
  case "$held" in
    1) printf 'busy\n'; exit 0 ;;
    0) ;;
    *) exit 2 ;;
  esac
done
printf 'free\n'`

func (e *Executor) waitPackageMaintenance(ctx context.Context) error {
	timeout, interval := e.MaintenanceTimeout, e.MaintenanceInterval
	if timeout <= 0 {
		timeout = 3 * time.Minute
	}
	if interval <= 0 {
		interval = 2 * time.Second
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	defer func() { e.job.Progress = ""; e.publish() }()
	timedOut := func() error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return &Problem{Code: "package-maintenance-timeout", Detail: fmt.Sprintf("The device's automatic package maintenance or dpkg lock did not clear within %s / 设备的自动软件包维护或 dpkg 锁在 %s 内未释放", timeout, timeout)}
	}
	for {
		if waitCtx.Err() != nil {
			return timedOut()
		}
		if err := e.Guard(waitCtx); err != nil {
			if waitCtx.Err() != nil {
				return timedOut()
			}
			return err
		}
		if e.Confirm != nil {
			if err := e.Confirm(waitCtx, e.dev); err != nil {
				if waitCtx.Err() != nil {
					return timedOut()
				}
				return err
			}
		}
		probeCtx, stop := context.WithTimeout(waitCtx, 10*time.Second)
		r, err := e.run(probeCtx, packageMaintenanceProbe)
		stop()
		if waitCtx.Err() != nil {
			return timedOut()
		}
		if err != nil {
			return fmt.Errorf("package maintenance probe / 软件包维护检查: %w", err)
		}
		if r.ExitCode != 0 {
			return &Problem{Code: "package-maintenance-probe", Detail: commandDetails(r)}
		}
		switch strings.TrimSpace(string(r.Stdout)) {
		case "free":
			return nil
		case "busy":
			if e.job.Progress == "" {
				e.job.Progress = PackageMaintenanceMessage
				e.log(PackageMaintenanceMessage + "\n")
			}
		default:
			return &Problem{Code: "package-maintenance-probe", Detail: "Unrecognized probe response / 无法识别检查结果: " + commandDetails(r)}
		}
		if err := pause(waitCtx, interval); err != nil {
			return timedOut()
		}
	}
}

type commandError struct {
	exit    int
	detail  string
	lock    bool
	problem *Problem
}

func (e *commandError) Error() string {
	if e.problem != nil {
		return e.problem.Error()
	}
	detail := e.detail
	if detail == "" {
		detail = "The device returned no diagnostic output / 设备未返回错误说明"
	}
	return fmt.Sprintf("exit %d: %s", e.exit, detail)
}

func (e *commandError) Unwrap() error {
	if e.problem != nil {
		return e.problem
	}
	return nil
}

func dpkgLockError(output string) bool {
	text := strings.ToLower(output)
	if !strings.Contains(text, "dpkg") || !strings.Contains(text, "lock") {
		return false
	}
	for _, contention := range []string{"another process", "held by process", "is locked", "was locked", "could not get lock", "unable to acquire", "resource temporarily unavailable"} {
		if strings.Contains(text, contention) {
			return true
		}
	}
	return false
}

// Legacy adb merges stderr into stdout. Preserve a stage's explicit failure marker from either
// stream and bounded final output, so job errors remain useful with both adb protocols and SSH.
func commandDetails(r adb.Result) string {
	var parts []string
	for _, stream := range []string{string(r.Stdout), string(r.Stderr)} {
		stream = strings.TrimSpace(strings.ReplaceAll(stream, "\r\n", "\n"))
		if stream == "" {
			continue
		}
		lines := strings.Split(stream, "\n")
		tail := strings.Join(lines[max(0, len(lines)-8):], "\n")
		if len(tail) > 4096 {
			tail = tail[len(tail)-4096:]
		}
		for i := len(lines) - 1; i >= 0; i-- {
			if strings.HasPrefix(lines[i], "OXP-STAGE ") && strings.Contains(lines[i], " failed") {
				marker := lines[i][:min(len(lines[i]), 4096)]
				if !strings.Contains(tail, marker) {
					parts = append(parts, marker)
				}
				break
			}
		}
		parts = append(parts, tail)
	}
	return strings.Join(parts, "\n")
}
