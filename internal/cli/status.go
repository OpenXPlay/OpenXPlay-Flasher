// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-22

package cli

import (
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/app"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/profile"
)

func printStatus(w io.Writer, state app.DeviceStatus) {
	if len(state.Candidates) == 0 {
		fmt.Fprintln(w, "No device found / 未找到设备")
	}
	for _, c := range state.Candidates {
		kind := "Supported / 支持"
		if !c.Supported {
			kind = "Ignored / 已忽略"
		}
		serial := c.Serialno
		if serial == "" {
			serial = c.ADBSerial
		}
		if serial == "" {
			serial = c.USB.Serial
		}
		mode := string(c.Mode)
		if c.StockDesignated {
			mode = "stock adb (designated) / 原厂 adb（已指定）"
		} else if c.Mode == profile.ModeADB && c.Supported {
			mode = "Linux adb"
		}
		fmt.Fprintf(w, "%s: %s %s model=%s serial=%s product=%s at %s\n", kind, strings.Join(c.Profiles, ","), mode, c.ADBName, serial, c.Product, c.USB.Location())
		if c.ReadError != "" {
			fmt.Fprintf(w, "  Read error / 读取错误: %s\n", c.ReadError)
		}
	}
	if state.Target != nil {
		fmt.Fprintf(w, "Profile / 设备配置: %s; mode / 模式: %s; serial / 序列号: %s\n", state.Target.Profile, state.Target.Device.Mode, state.Serial)
	}
	if state.Catalog.Verified {
		fmt.Fprintln(w, "Package list: verified / 软件包列表：已校验签名")
	} else {
		fmt.Fprintf(w, "Package list unavailable; run catalog fetch / 软件包列表不可用，请运行 catalog fetch: %s\n", state.Catalog.Reason)
	}
	printVersions(w, "Installed / 已安装", state.Installed)
	printVersions(w, "Recommended / 推荐版本", state.Recommended)
	fmt.Fprintf(w, "Suggested choice / 建议方案: %s\n", state.Suggested)
	for _, c := range state.Choices {
		erases := "no / 否"
		if c.Destructive {
			erases = "yes / 是"
		}
		fmt.Fprintf(w, "  %s: erases device / 擦除设备: %s; available / 可用: %t", c.ID, erases, c.Available)
		if c.Problem != nil {
			fmt.Fprintf(w, " (%s)", c.Problem)
		}
		fmt.Fprintln(w)
	}
	if state.Problem != nil {
		fmt.Fprintf(w, "Device status / 设备状态: %s\n", state.Problem)
	}
}

func printVersions(w io.Writer, label string, versions map[string]string) {
	for _, layer := range slices.Sorted(maps.Keys(versions)) {
		version := versions[layer]
		if version == "" {
			version = "absent / 未安装"
		}
		fmt.Fprintf(w, "%s %s: %s\n", label, layer, version)
	}
}

func printFiles(w io.Writer, state app.ChoiceFiles) {
	fmt.Fprintf(w, "Files for / 方案所需文件: %s (%s)\n", state.Choice, state.Profile)
	for _, f := range state.Files {
		status := "Missing / 缺少"
		if f.Status == "found" {
			status = "Found / 已找到"
		}
		fmt.Fprintf(w, "%s %s/%s %s (%d B)\n  SHA-256: %s\n", status, f.Layer, f.Role, f.Name, f.Size, f.SHA256)
		if f.Path != "" {
			fmt.Fprintf(w, "  Path / 路径: %s\n", f.Path)
		}
		for _, url := range f.URLs {
			fmt.Fprintf(w, "  %s\n", url)
		}
	}
}
