// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package app

import "golang.org/x/sys/unix"

func osVersion() string {
	v, _ := unix.Sysctl("kern.osproductversion")
	return v
}
