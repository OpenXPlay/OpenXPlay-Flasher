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

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/app"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/device"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/gui"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/usb"
)

const usage = `Usage: openxplay-flasher [command] [flags]

Commands:
  (none), app      start the desktop app
  version          print the build identity
  devices          list candidate devices once
  watch            print candidate devices whenever they change (Ctrl-C to stop)
  profiles         list the supported device models and their layers
  web              serve the app's page to a browser on this computer, for UI work

Flags:
  -json            devices, version, watch: print JSON instead of text
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

	bus := usb.NewLibusbBus()
	defer bus.Close()
	svc, err := app.NewForBus(bus, printer{out: stdout, json: *asJSON})
	if err != nil {
		fmt.Fprintln(stderr, "openxplay-flasher:", err)
		return 1
	}

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
