// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"time"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/app"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/stage"
)

func runInstall(svc *app.Service, files []string, only string, downgrade, yes bool, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(files) == 0 || only != "" && only != "device" && only != "relay" {
		fmt.Fprint(stderr, usage)
		return 2
	}
	choice := only
	if choice == "" {
		choice = "full"
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	svc.Start(ctx)
	defer svc.Stop()
	for _, filename := range files {
		if _, err := svc.InstallAdd(filename); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	}
	state := svc.Install(ctx)
	var plan stage.Choice
	for _, p := range state.Choices {
		if p.ID == choice {
			plan = p
		}
	}
	if state.Problem != nil {
		fmt.Fprintln(stderr, state.Problem)
		return 1
	}
	if plan.Problem != nil {
		printPlanProblem(stderr, plan.Problem)
		return 1
	}
	for _, need := range plan.Needs {
		if need.Problem != nil && need.Problem.Code != "downgrade" {
			printPlanProblem(stderr, need.Problem)
			return 1
		}
		if need.Package == nil {
			printPlanProblem(stderr, &stage.Problem{Code: "no-package", Detail: need.Layer})
			return 1
		}
	}
	fmt.Fprintln(stdout, "Install plan:")
	for i, step := range plan.Steps {
		fmt.Fprintf(stdout, "  %d. %s: %s\n", i+1, step.Layer, step.Action)
	}
	if plan.Downgrade {
		fmt.Fprintln(stdout, "Warning: this installs an older relay version.")
		if !downgrade {
			fmt.Fprintln(stderr, "Use -downgrade to allow this plan.")
			return 1
		}
	}
	fmt.Fprintln(stdout, "Bluetooth and the relay restart; the device layer reboots the device once. CarPlay stops for a while. Do not install while driving. Keep the USB cable connected.")
	if !yes {
		fmt.Fprint(stdout, "Start installation? Type y: ")
		if !confirmInstall(ctx, stdin) {
			fmt.Fprintln(stderr, "Installation cancelled / 已取消安装")
			return 1
		}
	}
	if _, err := svc.InstallStart(ctx, stage.Request{Choice: choice, Downgrade: downgrade}); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	state, err := svc.InstallWait(ctx)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	job := state.Job
	if job == nil {
		fmt.Fprintln(stderr, "No install job was started.")
		return 1
	}
	if job.Status != "succeeded" {
		fmt.Fprintf(stderr, "Installation failed at step %d: %v; device state: %s\n", job.Failed+1, job.Problem, job.Left)
		fmt.Fprint(stderr, job.Log)
		return 1
	}
	fmt.Fprintf(stdout, "Installation succeeded in %s.\n", job.Finished.Sub(job.Started).Round(time.Second))
	for _, id := range []string{"base", "device", "relay"} {
		fmt.Fprintf(stdout, "  %s: %s\n", id, job.Installed.Layers[id])
	}
	fmt.Fprintln(stdout, "Next: add identity.pk8 and certificate.p7b in the relay admin page, then set up networking. / 下一步：在中继管理页面添加 identity.pk8 和 certificate.p7b，再配置网络。")
	return 0
}

// A complete line confirms the prompt. EOF, a read error or Ctrl-C cancels it; the buffered
// channel lets the terminal reader finish if the caller's context is cancelled first.
func confirmInstall(ctx context.Context, stdin io.Reader) bool {
	if stdin == nil || ctx.Err() != nil {
		return false
	}
	answer := make(chan bool, 1)
	go func() {
		line, err := bufio.NewReader(stdin).ReadString('\n')
		answer <- err == nil && strings.TrimSpace(line) == "y"
	}()
	select {
	case confirmed := <-answer:
		return confirmed && ctx.Err() == nil
	case <-ctx.Done():
		return false
	}
}

func printPlanProblem(w io.Writer, p *stage.Problem) {
	if p.Code == "base-missing" {
		fmt.Fprintln(w, "Install the base system first. / 请先安装基础系统。")
		return
	}
	fmt.Fprintln(w, "Cannot install:", p)
}

type installPrinter struct {
	mu   sync.Mutex
	out  io.Writer
	last []string
}

func (p *installPrinter) Emit(name string, data any) {
	if name != app.EventInstall {
		return
	}
	s, ok := data.(app.InstallState)
	if !ok || s.Job == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for i, step := range s.Job.Steps {
		if i >= len(p.last) {
			p.last = append(p.last, "")
		}
		if p.last[i] == step.Status || step.Status == "pending" {
			continue
		}
		fmt.Fprintf(p.out, "%s: %s — %s\n", step.Layer, step.Action, step.Status)
		p.last[i] = step.Status
	}
}
