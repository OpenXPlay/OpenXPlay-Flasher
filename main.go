// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-20

// Command openxplay-flasher installs and updates the OpenXPlay relay on a USB dongle.
package main

import (
	"os"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/cli"
)

func main() {
	os.Exit(cli.Main(os.Args[1:], os.Stdout, os.Stderr))
}
