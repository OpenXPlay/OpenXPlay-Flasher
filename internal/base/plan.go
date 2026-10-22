// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package base

import (
	"slices"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/profile"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/stage"
)

type Identity struct {
	Mode     profile.Mode `json:"mode"`
	Product  string       `json:"product,omitempty"`
	Serial   string       `json:"serial"`
	Location string       `json:"location"`
}

// BuildRepairPlan continues an unfinished chain in place. Only journaled completions can
// remove steps; the backup, boot erase and bootloader restart must never precede this suffix.
func BuildRepairPlan(p profile.Profile, start Identity, previous *Journal) Plan {
	plan := BuildPlan(p, "repair", start)
	if !IncompleteChain(previous) {
		return plan
	}
	if p.Base == nil || previous.Profile != p.ID || previous.Serial != start.Serial || start.Mode != profile.ModeFastboot || start.Product != p.Base.Fastboot.Product {
		plan.Steps = []Step{}
		plan.Problem = &stage.Problem{Code: "chain-incomplete"}
		return plan
	}
	first := -1
	for i, s := range plan.Steps {
		if s.Window != "chain" {
			continue
		}
		if !slices.Contains(previous.Completed, s.ID) {
			if first < 0 {
				first = i
			}
		} else if first >= 0 {
			// A sequential window cannot have a completed step after a missing predecessor.
			plan.Steps = []Step{}
			plan.Problem = &stage.Problem{Code: "journal-invalid"}
			return plan
		}
	}
	if first >= 0 {
		plan.Steps = slices.Clone(plan.Steps[first:])
		return plan
	}
	plan.Steps = []Step{}
	plan.Problem = &stage.Problem{Code: "journal-invalid"}
	return plan
}

type Step struct {
	ID        string       `json:"id"`
	Action    string       `json:"action"`
	Partition string       `json:"partition,omitempty"`
	FileID    string       `json:"fileId,omitempty"`
	Product   string       `json:"product,omitempty"`
	Name      profile.Text `json:"name"`
	Critical  bool         `json:"critical"`
	Resumable bool         `json:"resumable"`
	Window    string       `json:"window,omitempty"`
}

type Plan struct {
	Kind    string         `json:"kind"`
	Serial  string         `json:"serial"`
	Start   Identity       `json:"start"`
	Steps   []Step         `json:"steps"`
	Problem *stage.Problem `json:"problem,omitempty"`
}

// BuildPlan is the procedure, in code: profile data can pin bytes but cannot add commands.
// A repair from stock LK must first bootstrap lk1st in the same aboot/erase order as stock.
func BuildPlan(p profile.Profile, kind string, start Identity) Plan {
	plan := Plan{Kind: kind, Serial: start.Serial, Start: start, Steps: []Step{}}
	bad := func(code string) Plan { plan.Problem = &stage.Problem{Code: code}; return plan }
	if p.Base == nil {
		return bad("base-unavailable")
	}
	if !ValidSerial(start.Serial) {
		return bad("bad-serial")
	}
	if kind != "stock" && kind != "system" && kind != "repair" {
		return bad("bad-choice")
	}
	b := p.Base
	stock := start.Mode == profile.ModeStock || start.Mode == profile.ModeFastboot && start.Product == b.Fastboot.StockProduct
	if kind == "stock" && !stock || kind == "system" && stock {
		return bad("wrong-mode")
	}
	if start.Mode != profile.ModeStock && start.Mode != profile.ModeADB && start.Mode != profile.ModeFastboot {
		return bad("wrong-mode")
	}
	if start.Mode == profile.ModeFastboot && start.Product != b.Fastboot.StockProduct && start.Product != b.Fastboot.Product {
		return bad("wrong-product")
	}
	add := func(id, action, partition, fileID, product, window, en, zh string) {
		critical := window != ""
		plan.Steps = append(plan.Steps, Step{ID: id, Action: action, Partition: partition, FileID: fileID, Product: product, Window: window, Critical: critical, Resumable: !critical, Name: profile.Text{"en": en, "zh-CN": zh}})
	}
	if start.Mode != profile.ModeFastboot {
		id, action := "confirm-linux", "confirm-linux"
		en, zh := "Confirm the Linux board serial", "确认 Linux 设备序列号"
		if stock {
			id, action = "confirm", "confirm-stock"
			en, zh = "Confirm the original firmware and serial", "确认原厂固件与序列号"
		}
		add(id, action, "", "", "", "", en, zh)
		add("adb-bootloader", "adb-bootloader", "", "", "", "", "Reboot into fastboot", "重启到 fastboot")
		product := b.Fastboot.Product
		if stock {
			product = b.Fastboot.StockProduct
		}
		add("wait-initial", "wait-fastboot", "", "", product, "", "Wait for fastboot and confirm the serial", "等待 fastboot 并确认序列号")
	}
	if stock {
		product := b.Fastboot.StockProduct
		add("bootstrap-aboot", "flash", "aboot", "aboot", product, "bootstrap", "Install lk1st aboot; do not unplug", "安装 lk1st aboot，请勿拔出设备")
		add("bootstrap-erase-boot", "erase", "boot", "", product, "bootstrap", "Erase boot after aboot; do not unplug", "在 aboot 写入后擦除 boot，请勿拔出设备")
		add("bootstrap-reboot", "reboot", "", "", product, "bootstrap", "Restart lk1st", "重启 lk1st")
		add("wait-lk1st", "wait-fastboot", "", "", b.Fastboot.Product, "", "Wait for lk1st and confirm the serial", "等待 lk1st 并确认序列号")
	}
	add("backup", "backup", "", "", b.Fastboot.Product, "", "Back up and compare this board's modem partitions", "备份并比对此设备的调制解调器分区")
	if kind != "system" {
		add("reset-boot", "erase", "boot", "", b.Fastboot.Product, "", "Erase boot before rebuilding the chain", "重建引导链前擦除 boot")
		add("restart-bootloader", "reboot-bootloader", "", "", b.Fastboot.Product, "", "Restart the bootloader", "重启引导程序")
		add("wait-chain", "wait-fastboot", "", "", b.Fastboot.Product, "", "Wait for lk1st before rebuilding", "等待 lk1st 以重建引导链")
		for _, entry := range [][2]string{{"partition", "gpt"}, {"hyp", "hyp"}, {"rpm", "rpm"}, {"sbl1", "sbl1"}, {"tz", "tz"}} {
			add("chain-"+entry[0], "flash", entry[0], entry[1], b.Fastboot.Product, "chain", "Write "+entry[0]+"; do not unplug", "写入 "+entry[0]+"，请勿拔出设备")
		}
		for _, name := range partitionNames {
			add("restore-"+name, "restore", name, "", b.Fastboot.Product, "chain", "Restore this board's "+name, "恢复此设备的 "+name)
		}
		for _, name := range []string{"aboot", "cdt"} {
			add("chain-"+name, "flash", name, name, b.Fastboot.Product, "chain", "Write "+name+"; do not unplug", "写入 "+name+"，请勿拔出设备")
		}
		for _, name := range []string{"boot", "rootfs"} {
			add("chain-erase-"+name, "erase", name, "", b.Fastboot.Product, "chain", "Erase "+name+"; do not unplug", "擦除 "+name+"，请勿拔出设备")
		}
		add("chain-reboot", "reboot", "", "", b.Fastboot.Product, "chain", "Restart after rebuilding the chain", "重建引导链后重启")
		add("wait-system", "wait-fastboot", "", "", b.Fastboot.Product, "", "Wait for lk1st before installing Linux", "等待 lk1st 以安装 Linux")
	}
	if kind == "system" {
		// With boot erased first, an interrupted rootfs write leaves lk1st in fastboot (Repair),
		// never an old kernel booting a half-written root filesystem.
		add("system-erase-boot", "erase", "boot", "", b.Fastboot.Product, "", "Erase boot before writing the system", "写入系统前擦除 boot")
	}
	add("rootfs", "flash", "rootfs", "rootfs", b.Fastboot.Product, "", "Install the root filesystem in pieces of at most 200 MiB", "以不超过 200 MiB 的分片安装根文件系统")
	add("boot", "flash", "boot", "boot", b.Fastboot.Product, "", "Install the board boot image", "安装此型号的启动镜像")
	add("boot-linux", "reboot", "", "", b.Fastboot.Product, "", "Boot Linux", "启动 Linux")
	add("verify-linux", "wait-linux", "", "", "", "", "Wait for Linux, confirm identity and record findings", "等待 Linux，确认身份并记录检查结果")
	return plan
}
