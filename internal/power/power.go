// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

// Package power prevents idle system sleep for the duration of an install job.
package power

type Inhibitor interface {
	Acquire() (release func(), err error)
}

type Noop struct{}

func (Noop) Acquire() (func(), error) { return func() {}, nil }
