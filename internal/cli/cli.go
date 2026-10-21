// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-20

// Package cli dispatches the command line: no command starts the desktop app, and the other
// commands are a developer shell over app.Service, for support and hardware-in-the-loop tests.
package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/adb"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/app"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/device"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/gui"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/power"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/usb"
)

const usage = `Usage: openxplay-flasher [command] [flags]

Commands:
  (none), app      start the desktop app
  version          print the build identity
  devices          list candidate devices once
  watch            print candidate devices whenever they change (Ctrl-C to stop)
  profiles         list the supported device models and their layers
  install [-only device|relay] [-downgrade] [-yes] PACKAGE...
                   install checked local layer packages over adb (asks for y without -yes)
  config-get PATH  print a configuration file of the connected device
  config-put PATH FILE
                   write FILE to PATH on the device (backup first; the relay restarts for
                   files its profile lists, or with -restart)
  web              serve the app's page to a browser on this computer, for UI work

Flags:
  -json            devices, version, watch: print JSON instead of text
  -restart         config-put: restart the relay after writing
  -addr host:port  web: loopback address to listen on (default 127.0.0.1:0)
`

// Main runs the command in args (without the program name) and returns the process exit code.
func Main(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "app" {
		if err := runGUI(); err != nil {
			fmt.Fprintln(stderr, "openxplay-flasher:", err)
			return 1
		}
		return 0
	}
	cmd := args[0]
	if cmd == "help" || cmd == "-h" || cmd == "--help" {
		fmt.Fprint(stdout, usage)
		return 0
	}
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	addr := fs.String("addr", "127.0.0.1:0", "loopback address for web")
	restart := fs.Bool("restart", false, "restart the relay after config-put")
	only := fs.String("only", "", "install only device or relay")
	downgrade := fs.Bool("downgrade", false, "allow an older relay version")
	yes := fs.Bool("yes", false, "install without asking for y")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if cmd == "web" {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		if err := gui.ServeWeb(ctx, *addr, stdout); err != nil {
			fmt.Fprintln(stderr, "openxplay-flasher:", err)
			return 1
		}
		return 0
	}

	bus := usb.NewHostBus()
	defer bus.Close()
	var events app.Events = printer{out: stdout, json: *asJSON}
	if cmd == "config-get" || cmd == "config-put" {
		events = quiet{} // their stdout is the file or the outcome, nothing else
	}
	if cmd == "install" {
		events = &installPrinter{out: stdout}
	}
	svc, err := app.NewForBus(bus, events)
	if err != nil {
		fmt.Fprintln(stderr, "openxplay-flasher:", err)
		return 1
	}
	svc.SetADB(adb.NewConnector())
	svc.SetSleepInhibitor(power.NewSystem())

	switch cmd {
	case "version":
		info := svc.Info()
		if *asJSON {
			return writeJSON(stdout, stderr, info)
		}
		fmt.Fprintf(stdout, "%s %s %s\n%s, %s\n", info.Name, info.Version, info.Commit, info.License, info.SourceURL)
	case "profiles":
		if *asJSON {
			return writeJSON(stdout, stderr, svc.Profiles())
		}
		for _, p := range svc.Profiles() {
			fmt.Fprintf(stdout, "%s  %s (%s)\n", p.ID, p.Name["en"], p.Family)
			for i, l := range p.Layers {
				note := ""
				if l.Destructive {
					note = ", erases the device"
				}
				fmt.Fprintf(stdout, "  %d. %-8s %s (%s%s)\n", i+1, l.ID, l.Name["en"], l.Transport, note)
			}
		}
	case "config-get", "config-put":
		return runConfig(svc, cmd, fs.Args(), *restart, stdout, stderr)
	case "install":
		return runInstall(svc, fs.Args(), *only, *downgrade, *yes, os.Stdin, stdout, stderr)
	case "devices":
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		snap := svc.Rescan(ctx)
		if *asJSON {
			return writeJSON(stdout, stderr, snap)
		}
		printSnapshot(stdout, snap)
		if snap.Error != "" {
			return 1
		}
	case "watch":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		svc.Start(ctx)
		<-ctx.Done()
		svc.Stop()
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n%s", cmd, usage)
		return 2
	}
	return 0
}

// runConfig reads or writes one configuration file of the connected device.
func runConfig(svc *app.Service, cmd string, args []string, restart bool, stdout, stderr io.Writer) int {
	if (cmd == "config-get" && len(args) != 1) || (cmd == "config-put" && len(args) != 2) {
		fmt.Fprint(stderr, usage)
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	// The service fills its device list on its first scan; a scan always sets Candidates or Error.
	svc.Start(ctx)
	defer svc.Stop()
	for snap := svc.Devices(); snap.Candidates == nil && snap.Error == "" && ctx.Err() == nil; snap = svc.Devices() {
		time.Sleep(50 * time.Millisecond)
	}
	if cmd == "config-get" {
		f, err := svc.ConfigRead(ctx, args[0])
		if err != nil {
			fmt.Fprintln(stderr, "openxplay-flasher:", err)
			return 1
		}
		if !f.Exists {
			fmt.Fprintf(stderr, "openxplay-flasher: %s does not exist\n", f.Path)
			return 1
		}
		fmt.Fprint(stdout, f.Content)
		return 0
	}
	data, err := os.ReadFile(args[1])
	if err != nil {
		fmt.Fprintln(stderr, "openxplay-flasher:", err)
		return 1
	}
	res, err := svc.ConfigWrite(ctx, args[0], string(data), restart)
	if err != nil {
		fmt.Fprintln(stderr, "openxplay-flasher:", err)
		return 1
	}
	switch {
	case res.RolledBack:
		fmt.Fprintf(stderr, "the relay did not stay up; the previous version was put back\n%s", res.Log)
		return 1
	case res.Restarted && !res.Healthy:
		fmt.Fprintf(stderr, "the relay did not stay up\n%s", res.Log)
		return 1
	case res.Restarted:
		fmt.Fprintf(stdout, "written (backup %s); the relay restarted and is running\n", res.Backup)
	default:
		fmt.Fprintf(stdout, "written (backup %s)\n", res.Backup)
	}
	return 0
}

type quiet struct{}

func (quiet) Emit(string, any) {}

type printer struct {
	out  io.Writer
	json bool
}

func (p printer) Emit(name string, data any) {
	snap, ok := data.(device.Snapshot)
	if name != app.EventDevices || !ok {
		return
	}
	if p.json {
		_ = json.NewEncoder(p.out).Encode(snap)
		return
	}
	fmt.Fprintf(p.out, "[%s]\n", time.Now().Format(time.TimeOnly))
	printSnapshot(p.out, snap)
}

func printSnapshot(w io.Writer, snap device.Snapshot) {
	if snap.Error != "" {
		fmt.Fprintf(w, "USB error: %s\n", snap.Error)
	}
	if len(snap.Candidates) == 0 {
		fmt.Fprintln(w, "no device found")
		return
	}
	for _, c := range snap.Candidates {
		what := "not a supported device"
		if c.Supported() {
			what = strings.Join(c.Profiles, " or ")
		}
		fmt.Fprintf(w, "%-8s %s at %-10s %s\n", c.Mode, c.USB.ID(), c.USB.Location(), what)
	}
}

func writeJSON(stdout, stderr io.Writer, v any) int {
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}
