// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package cli

import (
	"bufio"
	"errors"
	"fmt"
	"golang.org/x/sys/windows"
	"io"
	"os"
	"strings"
)

func terminalSecret(in io.Reader, out io.Writer, prompt string) (string, error) {
	f, ok := in.(*os.File)
	if !ok {
		return "", errors.New("Use OXP_FLASHER_SSH_PASSWORD with redirected input / 重定向输入时请使用 OXP_FLASHER_SSH_PASSWORD")
	}
	h := windows.Handle(f.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(h, &mode); err != nil {
		return "", errors.New("No console; set OXP_FLASHER_SSH_PASSWORD / 无控制台，请设置 OXP_FLASHER_SSH_PASSWORD")
	}
	if err := windows.SetConsoleMode(h, mode&^windows.ENABLE_ECHO_INPUT); err != nil {
		return "", err
	}
	defer windows.SetConsoleMode(h, mode)
	fmt.Fprint(out, prompt)
	value, err := bufio.NewReader(in).ReadString('\n')
	fmt.Fprintln(out)
	return strings.TrimRight(value, "\r\n"), err
}
