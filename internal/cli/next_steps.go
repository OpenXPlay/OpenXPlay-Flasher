// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-22

package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"time"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/app"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/stage"
)

// Injectable so the admin command's Ctrl-C lifecycle can be tested without process signals.
var nextStepsContext = func() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt)
}

func runNextStepsCLI(cmd string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stdout, usage) }
	var sshOptions sshFlags
	sshOptions.register(fs)
	if err := fs.Parse(interspersed(args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	positional := fs.Args()
	if cmd == "admin" && len(positional) != 0 || cmd == "identity" &&
		(len(positional) == 0 || positional[0] != "status" && positional[0] != "import" ||
			positional[0] == "status" && len(positional) != 1 || positional[0] == "import" && len(positional) < 2) {
		fmt.Fprint(stderr, usage)
		return 2
	}
	// These commands publish only their outcome. Service events, file paths and remote
	// diagnostics must never be formatted as installation or package output.
	svc, closeService, err := commandService(!sshOptions.enabled(), quiet{})
	if err != nil {
		return nextStepsFailure(stderr, err)
	}
	defer closeService()
	ctx, stop := nextStepsContext()
	defer stop()
	operation, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if err := sshOptions.connect(operation, svc, stdin, stderr); err != nil {
		return nextStepsFailure(stderr, err)
	}
	if cmd == "identity" {
		if positional[0] == "status" {
			state, err := svc.IdentityStatus(operation)
			if err != nil {
				return nextStepsFailure(stderr, err)
			}
			fmt.Fprintln(stdout, state.Identity)
		} else {
			if _, err := svc.IdentityImport(operation, positional[1:]); err != nil {
				return nextStepsFailure(stderr, err)
			}
			fmt.Fprintln(stdout, "Identity added / 已添加身份")
		}
		return 0
	}
	if sshOptions.enabled() {
		state, err := svc.IdentityStatus(operation)
		if err != nil {
			return nextStepsFailure(stderr, err)
		}
		fmt.Fprintln(stdout, state.AdminURL)
		return 0
	}
	forward, err := svc.ForwardStart(operation)
	if err != nil {
		return nextStepsFailure(stderr, err)
	}
	defer svc.ForwardStop()
	fmt.Fprintln(stdout, forward.Address)
	fmt.Fprintln(stderr, "Keep this command running; Ctrl-C closes the USB admin connection. / 请保持此命令运行；按 Ctrl-C 关闭 USB 管理连接。")
	<-ctx.Done()
	return 0
}

// Never print error details: a transport, file name or remote diagnostic may contain
// credential data. Only allowlisted codes and fixed bilingual messages reach the terminal.
func nextStepsFailure(w io.Writer, err error) int {
	code := "device"
	var problem *stage.Problem
	if errors.As(err, &problem) {
		code = problem.Code
	} else {
		for _, entry := range []struct {
			err  error
			code string
		}{
			{app.ErrNoDevice, "no-device"}, {app.ErrSeveralDevices, "several-devices"},
			{app.ErrNoADB, "no-adb"}, {app.ErrNotADB, "not-adb"},
			{app.ErrUnconfirmed, "unconfirmed"}, {app.ErrDeviceBusy, "busy"},
		} {
			if errors.Is(err, entry.err) {
				code = entry.code
				break
			}
		}
	}
	message, ok := nextStepsErrors[code]
	if !ok {
		code, message = "device", nextStepsErrors["device"]
	}
	fmt.Fprintf(w, "[%s] %s\n", code, message)
	return 1
}

var nextStepsErrors = map[string]string{
	"device":                "Could not reach the device. Check the connection and login, then try again. / 无法访问设备，请检查连接和登录信息后重试。",
	"no-device":             "Connect one supported Linux device. / 请连接一台支持的 Linux 设备。",
	"several-devices":       "Several supported devices are connected. Connect only one. / 已连接多台支持的设备，请只保留一台。",
	"no-adb":                "This build cannot reach devices over USB. / 此版本无法通过 USB 访问设备。",
	"not-adb":               "The device must be running Linux with adb or SSH. / 设备须运行 Linux，并通过 adb 或 SSH 连接。",
	"unconfirmed":           "The device model is not confirmed. / 尚未确认设备型号。",
	"busy":                  "A device operation is running. Wait for it to finish. / 设备操作正在进行，请等待完成。",
	"not-root":              "The device shell must run as root. / 设备命令行须以 root 权限运行。",
	"bad-serial":            "Could not confirm the board serial. / 无法确认设备序列号。",
	"serial-mismatch":       "The connected device changed. Reconnect and try again. / 连接的设备已变更，请重新连接后重试。",
	"relay-missing":         "Install the OpenXPlay relay first. / 请先安装 OpenXPlay 中继。",
	"forward-unavailable":   "Could not open the USB admin connection. Keep the device connected and try again. / 无法建立 USB 管理连接，请保持设备连接后重试。",
	"identity-unrecognized": "Choose a PKCS #8 private key and PKCS #7 / CMS certificate (DER or PEM). / 请选择 PKCS #8 私钥和 PKCS #7 / CMS 证书（DER 或 PEM）。",
	"identity-key-format":   "EC PRIVATE KEY is not PKCS #8. Choose a P-256 PKCS #8 key. / EC PRIVATE KEY 不是 PKCS #8，请选择 P-256 PKCS #8 私钥。",
	"identity-key-type":     "The PKCS #8 private key must be an EC key. / PKCS #8 私钥须为 EC 密钥。",
	"identity-key-curve":    "The EC private key must use P-256. / EC 私钥须使用 P-256 曲线。",
	"identity-encrypted":    "Choose unencrypted identity files or an unencrypted ZIP. / 请选择未加密的身份文件或 ZIP 压缩包。",
	"identity-missing":      "Select both the private key and certificate, or a ZIP containing both. / 请选择私钥和证书两个文件，或包含两者的 ZIP 压缩包。",
	"identity-duplicate":    "Select only one private key and one certificate. / 请只选择一份私钥和一份证书。",
	"identity-read":         "Could not read an identity file. Choose regular files no larger than 4 MiB each. / 无法读取身份文件，请选择每个不超过 4 MiB 的普通文件。",
	"identity-zip":          "Could not open the ZIP. Choose the two identity files or a valid ZIP. / 无法打开 ZIP，请选择两个身份文件或有效的 ZIP 压缩包。",
	"identity-key":          "The private key is not valid PKCS #8 data (DER or PEM). / 私钥不是有效的 PKCS #8 数据（DER 或 PEM）。",
	"identity-certificate":  "The certificate is not valid PKCS #7 / CMS SignedData (DER or PEM). / 证书不是有效的 PKCS #7 / CMS SignedData 数据（DER 或 PEM）。",
	"identity-write":        "Could not add the identity. Keep the device connected and retry with both files. / 无法添加身份文件，请保持设备连接，重新选择两个文件后重试。",
	"identity-status":       "Could not confirm the identity files. Keep the device connected and try again. / 无法确认身份文件，请保持设备连接后重试。",
}
