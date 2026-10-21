// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package base

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/adb"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/device"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/fastboot"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/profile"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/stage"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/usb"
)

// Fastboot is deliberately limited to what the base procedure uses. There is no arbitrary
// download/command escape hatch: every persistent write goes through Executor.write.
type Fastboot interface {
	GetVar(context.Context, string) (string, error)
	Flash(context.Context, string, *fastboot.Image, fastboot.FlashOptions, fastboot.Progress) error
	Erase(context.Context, string) error
	OEM(context.Context, string) (fastboot.Response, error)
	Upload(context.Context, io.Writer, fastboot.Progress) (int64, error)
	Reboot(context.Context) error
	RebootBootloader(context.Context) error
	Close() error
}
type OpenFastboot func(context.Context, usb.DeviceInfo) (Fastboot, error)

func NativeFastboot(ctx context.Context, d usb.DeviceInfo) (Fastboot, error) {
	return fastboot.Open(ctx, d)
}

// OneCandidate counts all fastboot devices, even unknown USB IDs, plus every supported family
// candidate. Unknown adb devices are ignored and never opened by automatic discovery.
func OneCandidate(snap device.Snapshot, mode profile.Mode) (*device.Candidate, error) {
	if snap.Error != "" {
		return nil, &stage.Problem{Code: "usb", Detail: snap.Error}
	}
	var found []device.Candidate
	for _, c := range snap.Candidates {
		if c.Mode == profile.ModeFastboot || c.Supported() || c.Mode == profile.ModeStock {
			found = append(found, c)
		}
	}
	if len(found) > 1 {
		return nil, &stage.Problem{Code: "several-devices"}
	}
	if len(found) == 0 || found[0].Mode != mode {
		return nil, &stage.Problem{Code: "no-device"}
	}
	return &found[0], nil
}

func ReadFastbootIdentity(ctx context.Context, fb Fastboot) (Identity, error) {
	product, err := fb.GetVar(ctx, "product")
	if err != nil {
		return Identity{}, err
	}
	serial, err := fb.GetVar(ctx, "serialno")
	if err != nil {
		return Identity{}, err
	}
	if !ValidSerial(serial) {
		return Identity{}, &stage.Problem{Code: "bad-serial"}
	}
	return Identity{Mode: profile.ModeFastboot, Product: product, Serial: serial}, nil
}

func shellText(ctx context.Context, d adb.Device, cmd string) (string, error) {
	r, err := d.Shell(ctx, cmd)
	if err != nil {
		return "", err
	}
	if r.ExitCode != 0 {
		return "", fmt.Errorf("identity command failed: %s (exit %d)", cmd, r.ExitCode)
	}
	return strings.TrimSpace(string(r.Stdout)), nil
}

// StockIdentity is called only after explicit designation. It reads the three profile model
// properties and release individually, and binds ro.serialno to the transport serial.
func StockIdentity(ctx context.Context, d adb.Device, p profile.Profile, transportSerial string) (Identity, error) {
	if p.Base == nil {
		return Identity{}, &stage.Problem{Code: "base-unavailable"}
	}
	values := []string{}
	for _, key := range []string{"ro.product.model", "ro.product.name", "ro.product.device", "ro.build.version.release", "ro.serialno"} {
		got, err := shellText(ctx, d, "getprop "+key)
		if err != nil {
			return Identity{}, err
		}
		values = append(values, key+"="+got)
		want := p.Base.Stock.Properties[key]
		if want != "" && got != want || key == "ro.build.version.release" && !strings.HasPrefix(got, p.Base.Stock.ReleasePrefix) {
			return Identity{}, &stage.Problem{Code: "stock-mismatch", Detail: strings.Join(values, ", ")}
		}
		if key == "ro.serialno" {
			if !ValidSerial(got) || transportSerial == "" || got != transportSerial {
				return Identity{}, &stage.Problem{Code: "serial-mismatch", Detail: strings.Join(values, ", ")}
			}
			return Identity{Mode: profile.ModeStock, Serial: got}, nil
		}
	}
	return Identity{}, &stage.Problem{Code: "stock-mismatch"}
}

func LinuxSerial(ctx context.Context, d adb.Device) (string, error) {
	cmdline, err := shellText(ctx, d, "cat /proc/cmdline")
	if err != nil {
		return "", err
	}
	serial := ""
	for _, part := range strings.Fields(cmdline) {
		if v, ok := strings.CutPrefix(part, "androidboot.serialno="); ok {
			if serial != "" {
				return "", &stage.Problem{Code: "bad-serial"}
			}
			serial = v
		}
	}
	if !ValidSerial(serial) {
		return "", &stage.Problem{Code: "bad-serial"}
	}
	return serial, nil
}
