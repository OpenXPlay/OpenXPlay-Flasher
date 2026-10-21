// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"time"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/adb"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/app"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/base"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/power"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/stage"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/usb"
)

// commandService is injectable so command tests never construct a host bus or use hardware.
var commandService = func(hardware bool, events app.Events) (*app.Service, func(), error) {
	var bus usb.Bus = usb.NewFakeBus()
	if hardware {
		bus = usb.NewHostBus()
	}
	svc, err := app.NewForBus(bus, events)
	if err != nil {
		bus.Close()
		return nil, func() {}, err
	}
	if err = svc.ConfigureDefaultStorage(); err != nil {
		bus.Close()
		return nil, func() {}, err
	}
	svc.SetADB(adb.NewConnector())
	svc.SetSleepInhibitor(power.NewSystem())
	return svc, func() { svc.Stop(); bus.Close() }, nil
}

// interspersed preserves the support-friendly COMMAND ARG --flag VALUE spelling with flag's
// standard parser. Boolean flags never consume the following positional argument.
func interspersed(args []string) []string {
	var flags, rest []string
	booleans := map[string]bool{"json": true, "yes": true, "downgrade": true, "no-signature-check": true, "ssh-usb": true, "restart": true}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			rest = append(rest, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			rest = append(rest, arg)
			continue
		}
		flags = append(flags, arg)
		key := strings.TrimLeft(arg, "-")
		if !strings.Contains(key, "=") && !booleans[key] && key != "h" && key != "help" && i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}
	return append(flags, rest...)
}

func runEngineCLI(cmd string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	var sshOptions sshFlags
	sshOptions.register(fs)
	restart := fs.Bool("restart", false, "restart the relay after writing / 写入后重启中继")
	asJSON := fs.Bool("json", false, "print JSON / 输出 JSON")
	channel := fs.String("channel", "beta", "catalog channel / 目录频道")
	var noSignatureCheck bool
	if cmd == "catalog" || cmd == "import" || cmd == "backup" || cmd == "plan" || cmd == "install" {
		fs.BoolVar(&noSignatureCheck, "no-signature-check", false, "ignore the package list signature; keep file SHA-256 checks / 忽略软件包列表签名，仍校验文件 SHA-256")
	}
	serial := fs.String("serial", "", "board serial / 设备序列号")
	basePlan := fs.String("base-plan", "", "repair: rebuild the chain during full reinstall / 完整重装时重建引导链")
	choice := fs.String("choice", "", "stock, base, full-reinstall, repair, full, device or relay / 安装方案")
	only := fs.String("only", "", "device or relay (local-package compatibility) / 兼容本地安装参数")
	yesErase := fs.String("yes-erase", "", "confirm erasure with this board's serial / 输入设备序列号以确认擦除")
	stockADB := fs.String("stock-adb", "", "explicitly designate this stock adb serial / 明确指定原厂 adb 序列号")
	stockLocation := fs.String("stock-location", "", "explicitly designate this USB location / 明确指定 USB 端口")
	downgrade := fs.Bool("downgrade", false, "allow an older relay / 允许旧版中继")
	yes := fs.Bool("yes", false, "skip the y prompt (never the serial check) / 跳过 y 提示，仍须验证序列号")
	if err := fs.Parse(interspersed(args)); err != nil {
		return 2
	}
	positional := fs.Args()
	usageError := func() int { fmt.Fprint(stderr, usage); return 2 }
	switch cmd {
	case "catalog":
		if len(positional) != 1 || positional[0] != "fetch" && positional[0] != "show" {
			return usageError()
		}
	case "import":
		if len(positional) == 0 {
			return usageError()
		}
	case "backup":
		if len(positional) == 0 {
			return usageError()
		}
		op := positional[0]
		if op == "list" && len(positional) != 1 || (op == "show" || op == "verify") && len(positional) != 2 || op == "import" && (len(positional) != 2 || *serial == "") || op != "list" && op != "show" && op != "verify" && op != "import" {
			return usageError()
		}
	case "fastboot":
		if len(positional) != 2 || positional[0] != "getvar" {
			return usageError()
		}
	case "designate-stock":
		if len(positional) != 1 {
			return usageError()
		}
	case "devices":
		if len(positional) != 0 {
			return usageError()
		}
	case "config-get":
		if len(positional) != 1 {
			return usageError()
		}
	case "config-put":
		if len(positional) != 2 {
			return usageError()
		}
	case "plan", "install":
		if *choice == "" {
			*choice = *only
		}
		if *choice == "" {
			*choice = "full"
		}
		if *only != "" && *only != "device" && *only != "relay" {
			return usageError()
		}
		valid := false
		for _, id := range []string{"stock", "base", "full-reinstall", "repair", "full", "device", "relay"} {
			valid = valid || id == *choice
		}
		if !valid {
			return usageError()
		}
	}
	hardware := !sshOptions.enabled() && (cmd == "fastboot" || cmd == "designate-stock" || cmd == "plan" || cmd == "install" || cmd == "devices" || cmd == "config-get" || cmd == "config-put") || sshOptions.usb
	if sshOptions.enabled() && cmd != "devices" && cmd != "plan" && cmd != "install" && cmd != "config-get" && cmd != "config-put" {
		return usageError()
	}
	var events app.Events = &enginePrinter{out: stderr, json: *asJSON}
	svc, closeService, err := commandService(hardware, events)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer closeService()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	svc.SetSignatureCheck(ctx, !noSignatureCheck)
	failure := func(err error) int {
		if *asJSON {
			v := map[string]any{"error": err.Error()}
			var p *stage.Problem
			if errors.As(err, &p) {
				v["code"] = p.Code
				v["detail"] = p.Detail
			}
			returnCode := writeJSON(stdout, stderr, v)
			if returnCode != 0 {
				return returnCode
			}
			return 1
		}

		var p *stage.Problem
		if errors.As(err, &p) {
			switch p.Code {
			case "backup-restore-set":
				fmt.Fprintf(stderr, "Backup set %s is missing or invalid. Import a backup before continuing. / 备份集 %s 缺失或无效，请先导入备份。\n", p.Detail, p.Detail)
			case "unmatched-file":
				fmt.Fprintf(stderr, "This file is not one of the files listed here / 此文件不在此处列出的文件中: %s\n", p.Detail)
			case "catalog-unknown":
				fmt.Fprintf(stderr, "This package is not in the package list. Listed versions / 此安装包不在软件包列表中。列表包含以下版本: %s\n", p.Detail)
			case "file-changed":
				fmt.Fprintf(stderr, "This file changed after selection; choose it again / 此文件在选择后发生变化，请重新选择: %s\n", p.Detail)
			case "file-cancelled":
				fmt.Fprintf(stderr, "File checking cancelled / 已取消文件校验: %s\n", p.Detail)
			default:
				fmt.Fprintf(stderr, "Cannot continue / 无法继续: %v\n", err)
			}
		} else {
			fmt.Fprintf(stderr, "Cannot continue / 无法继续: %v\n", err)
		}

		return 1
	}
	if err = sshOptions.connect(ctx, svc, stdin, stderr); err != nil {
		return failure(err)
	}
	if cmd == "devices" {
		snap := svc.Rescan(ctx)
		if *asJSON {
			return writeJSON(stdout, stderr, snap)
		}
		printSnapshot(stdout, snap)
		if snap.Error != "" {
			return 1
		}
		return 0
	}
	if cmd == "config-get" || cmd == "config-put" {
		return runConfig(svc, cmd, positional, *restart, stdout, stderr)
	}
	fetch := func() {
		fetchCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		_, _ = svc.CatalogFetch(fetchCtx, *channel)
	}
	show := func() { _, _ = svc.CatalogShow(ctx) }
	output := func(v any) int { return writeEngineResult(stdout, stderr, v, *asJSON) }

	switch cmd {
	case "catalog":
		var state app.CatalogState
		if positional[0] == "fetch" {
			state, err = svc.CatalogFetch(ctx, *channel)
		} else {
			state, err = svc.CatalogShow(ctx)
		}
		if err != nil {
			return failure(err)
		}
		return output(state)
	case "import":
		fetch()

		state, err := svc.SelectFiles(ctx, "", "", "", positional)
		if err != nil {
			return failure(err)
		}
		for _, result := range state.FileResults {
			if result.Problem != nil {
				return failure(result.Problem)
			}
		}
		return output(state)
	case "backup":
		op := positional[0]
		dir := ""
		if op == "show" || op == "verify" {
			*serial = positional[1]
		}
		if op == "import" {
			dir = positional[1]
		}
		if op == "verify" || op == "import" {
			fetch()
		} else {
			show()
		}
		result, err := svc.BackupOperation(ctx, op, *serial, dir)
		if err != nil {
			return failure(err)
		}
		return output(result)
	case "fastboot":
		readCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		value, err := svc.FastbootGetVar(readCtx, positional[1])
		if err != nil {
			return failure(err)
		}
		if *asJSON {
			return output(map[string]string{"name": positional[1], "value": value})
		}
		fmt.Fprintf(stdout, "%s: %s\n", positional[1], value)
		return 0
	case "designate-stock":
		state, err := svc.DesignateStock(ctx, positional[0])
		if err != nil {
			return failure(err)
		}
		return output(state)
	}
	if cmd == "plan" {
		show()
	} else {
		fetch()
	}
	// CLI designation authorizes only the named transport for this invocation.
	if *stockLocation != "" {
		if _, err = svc.DesignateStock(ctx, *stockLocation); err != nil {
			return failure(err)
		}
	}
	if *stockADB != "" {
		if _, err = svc.DesignateStockSerial(ctx, *stockADB); err != nil {
			return failure(err)
		}
	}

	if len(positional) > 0 {
		state, err := svc.SelectFiles(ctx, *choice, "", "", positional)
		if err != nil {
			return failure(err)
		}
		for _, result := range state.FileResults {
			if result.Problem != nil {
				return failure(result.Problem)
			}
		}
	}

	state := svc.Install(ctx)
	if cmd == "plan" {
		state, err = svc.InstallPrepare(ctx, *choice)
		if err != nil {
			return failure(err)
		}
	}
	var selected *stage.Choice
	for _, c := range state.Choices {
		if c.ID == *choice {
			copy := c
			selected = &copy
			break
		}
	}
	if selected == nil {
		return failure(&stage.Problem{Code: "bad-choice", Detail: *choice})
	}
	if *basePlan != "" {
		changed, err := svc.InstallPlan(stage.Request{Choice: *choice, BasePlan: *basePlan})
		if err != nil {
			return failure(err)
		}
		selected = &changed
	}

	if cmd == "plan" {
		if *asJSON {
			return output(selected)
		}
		printEnginePlan(stdout, *selected)
		if selected.Problem != nil {
			return failure(selected.Problem)
		}
		return 0
	}
	if state.Problem != nil {
		return failure(state.Problem)
	}
	if selected.Problem != nil {
		return failure(selected.Problem)
	}
	if selected.Destructive && (*yesErase == "" || *yesErase != selected.Serial || !base.ValidSerial(*yesErase)) {
		return failure(&stage.Problem{Code: "typed-serial", Detail: "use --yes-erase SERIAL / 请使用 --yes-erase 设备序列号"})
	}
	if _, err = svc.InstallPrepare(ctx, *choice); err != nil {
		return failure(err)
	}
	state = svc.Install(ctx)
	if !*asJSON {
		printEnginePlan(stdout, *selected)
		if sshOptions.enabled() {
			fmt.Fprintln(stdout, "Keep the box powered and connected. / 请保持盒子供电和连接。")
		} else {
			fmt.Fprintln(stdout, "Keep the USB cable connected and this computer powered. / 请保持 USB 连接和电脑供电。")
		}
	}
	if !*yes && !selected.Destructive {
		if *asJSON {
			return failure(&stage.Problem{Code: "confirmation", Detail: "use --yes / 请使用 --yes"})
		}
		fmt.Fprint(stdout, "Start installation? Type y / 开始安装？输入 y: ")
		answer, _ := bufio.NewReader(stdin).ReadString('\n')
		if strings.TrimSpace(answer) != "y" {
			fmt.Fprintln(stdout, "Cancelled / 已取消")
			return 0
		}
	}
	svc.Start(ctx)
	if _, err = svc.InstallStart(ctx, stage.Request{Choice: *choice, Downgrade: *downgrade, YesErase: *yesErase, BasePlan: *basePlan}); err != nil {
		return failure(err)
	}
	// Always wait through critical windows even after Ctrl-C; Stop also waits before releasing
	// power inhibition. Cancellation is honored at the next resumable boundary.
	state, err = svc.InstallWait(context.Background())
	if err != nil {
		return failure(err)
	}
	if *asJSON {
		code := output(state)
		if code != 0 {
			return code
		}
		if state.Job == nil || state.Job.Status != "succeeded" {
			return 1
		}
		return 0
	}
	if state.Job == nil {
		return failure(&stage.Problem{Code: "no-job"})
	}
	if state.Job.Status != "succeeded" {
		return failure(state.Job.Problem)
	}
	fmt.Fprintln(stdout, "Installation succeeded. / 安装成功。")
	return 0
}
func printEnginePlan(w io.Writer, c stage.Choice) {
	name := c.ID
	if c.Name != nil {
		name = c.Name["en"] + " / " + c.Name["zh-CN"]
	}
	fmt.Fprintln(w, "Install plan / 安装方案:", name)
	for i, s := range c.Steps {
		label := s.Action
		if s.Name != nil {
			label = s.Name["en"] + " / " + s.Name["zh-CN"]
		}
		phase := "resumable / 可恢复"
		if s.Critical {
			phase = "critical; do not unplug / 关键阶段，请勿拔出"
		}
		fmt.Fprintf(w, "  %d. %s: %s [%s]\n", i+1, s.Layer, label, phase)
	}
}

type enginePrinter struct {
	mu   sync.Mutex
	out  io.Writer
	json bool
	last string
	at   time.Time
	seen map[string]string
}

func (p *enginePrinter) Emit(name string, data any) {
	if name != app.EventInstall {
		return
	}
	state, ok := data.(app.InstallState)
	if !ok {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if state.Job != nil {
		for _, step := range state.Job.Steps {
			if step.Status == "running" {
				key := step.ID + step.Layer + step.Action
				if key == p.last {
					return
				}
				p.last = key
				if p.json {
					_ = json.NewEncoder(p.out).Encode(map[string]any{"type": "step", "step": step})
				} else {
					fmt.Fprintf(p.out, "%s: %s / 正在执行\n", step.Layer, step.ID+step.Action)
				}
				return
			}
		}
	}

	if check := state.FileCheck; check != nil && check.Status == "checking" {
		if p.last == check.Name && time.Since(p.at) < 200*time.Millisecond && check.Done != check.Total {
			return
		}
		p.last, p.at = check.Name, time.Now()
		if p.json {
			_ = json.NewEncoder(p.out).Encode(map[string]any{"type": "file-check", "check": check})
		} else {
			fmt.Fprintf(p.out, "Checking / 正在校验 %s: %d/%d B\n", check.Name, check.Done, check.Total)
		}
		return
	}

	if p.seen == nil {
		p.seen = map[string]string{}
	}
	for _, a := range state.Artifacts {
		if a.Done == 0 {
			continue
		}
		key := a.SHA256
		value := fmt.Sprintf("%d/%t/%v", a.Done, a.Verified, a.Error)
		if p.seen[key] == value {
			continue
		}
		if !a.Verified && key == p.last && time.Since(p.at) < 200*time.Millisecond {
			continue
		}
		p.last = key
		p.at = time.Now()
		p.seen[key] = value
		if p.json {
			_ = json.NewEncoder(p.out).Encode(map[string]any{"type": "file-check", "artifact": a})
		} else {
			fmt.Fprintf(p.out, "%s: %d/%d bytes / 字节\n", a.Name, a.Done, a.Total)
		}
		return
	}
}

func writeEngineResult(stdout, stderr io.Writer, value any, asJSON bool) int {
	if asJSON {
		return writeJSON(stdout, stderr, value)
	}
	switch v := value.(type) {
	case []app.BackupListing:
		if len(v) == 0 {
			fmt.Fprintln(stdout, "No backups / 没有备份")
		}
		for _, listing := range v {
			printBackups(stdout, listing)
		}
	case app.BackupListing:
		printBackups(stdout, v)
	case app.CatalogState:
		fmt.Fprintf(stdout, "Catalog / 目录 %s: %s\n", v.Channel, v.Status)
		if v.Verified {
			fmt.Fprintln(stdout, "Package list: verified / 软件包列表：已校验签名")
		} else {
			reason := v.Reason
			switch reason {
			case "signature off":
				reason += " / 签名校验已关闭"
			case "no signature":
				reason += " / 没有签名"
			case "bad signature":
				reason += " / 签名无效"
			}
			fmt.Fprintf(stdout, "Package list: not verified / 软件包列表：未校验签名 (%s)\n", reason)
		}
		if v.Index != nil {
			fmt.Fprintf(stdout, "Sequence / 序号: %d\n", v.Index.Sequence)
			for id, d := range v.Index.Devices {
				for layer, version := range d.Recommended {
					fmt.Fprintf(stdout, "  %s %s: %s\n", id, layer, version)
				}
			}
		}
	case app.InstallState:
		if v.Identity != nil {
			fmt.Fprintf(stdout, "Board / 设备: %s (%s)\n", v.Identity.Serial, v.Identity.Mode)
		}
		for _, a := range v.Artifacts {
			fmt.Fprintf(stdout, "%s: %d/%d bytes / 字节; %s; verified / 已验证: %t\n  SHA-256 %s\n", a.Name, a.Done, a.Total, a.Source, a.Verified, a.SHA256)
		}
		for _, p := range v.Packages {
			fmt.Fprintf(stdout, "%s %s: SHA-256 %s\n", p.Layer, p.Version, p.SHA256)
		}
	case base.Backup:
		fmt.Fprintf(stdout, "Backup / 备份: %s %s %s %s\n", v.Profile, v.Serial, v.Set, v.Created.UTC().Format(time.RFC3339Nano))
		for _, name := range []string{"fsc", "fsg", "modemst1", "modemst2"} {
			p := v.Partitions[name]
			fmt.Fprintf(stdout, "%s: %d bytes / 字节; SHA-256 %s; %s\n", name, p.Size, p.SHA256[:min(12, len(p.SHA256))], p.Source)
		}
	default:
		return writeJSON(stdout, stderr, value)
	}
	return 0
}

func printBackups(w io.Writer, listing app.BackupListing) {
	fmt.Fprintf(w, "%s %s\n", listing.Profile, listing.Serial)
	if listing.RepairSet == "" {
		fmt.Fprintln(w, "  Repair uses the next fresh capture. / 修复将使用下次新捕获的备份。")
	} else {
		fmt.Fprintf(w, "  Repair backup set / 修复使用的备份集: %s\n", listing.RepairSet)
	}
	for _, set := range listing.Sets {
		fmt.Fprintf(w, "  %s  %s\n", set.Set, set.Created.UTC().Format(time.RFC3339Nano))
		for _, name := range []string{"fsc", "fsg", "modemst1", "modemst2"} {
			p := set.Partitions[name]
			fmt.Fprintf(w, "    %s: %d bytes / 字节  SHA-256 %s  source / 来源: %s\n", name, p.Size, p.ShortSHA256, p.Source)
		}
	}
}
