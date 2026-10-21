// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

//go:build (!darwin && !windows) || (darwin && !cgo)

package power

func NewSystem() Inhibitor { return Noop{} }
