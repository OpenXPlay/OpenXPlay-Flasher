//go:build darwin || linux

// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

func terminalSecret(in io.Reader, out io.Writer, prompt string) (string, error) {
	f, ok := in.(*os.File)
	if !ok {
		return "", errors.New("Use OXP_FLASHER_SSH_PASSWORD with redirected input / 重定向输入时请使用 OXP_FLASHER_SSH_PASSWORD")
	}
	fd := int(f.Fd())
	old, err := unix.IoctlGetTermios(fd, termGet)
	if err != nil {
		return "", errors.New("No terminal; set OXP_FLASHER_SSH_PASSWORD / 无终端，请设置 OXP_FLASHER_SSH_PASSWORD")
	}
	next := *old
	next.Lflag &^= unix.ECHO
	if err = unix.IoctlSetTermios(fd, termSet, &next); err != nil {
		return "", err
	}
	defer unix.IoctlSetTermios(fd, termSet, old)
	fmt.Fprint(out, prompt)
	value, err := bufio.NewReader(in).ReadString('\n')
	fmt.Fprintln(out)
	return strings.TrimRight(value, "\r\n"), err
}
