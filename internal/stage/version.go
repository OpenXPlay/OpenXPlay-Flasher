// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package stage

import (
	"cmp"
	"regexp"
	"strings"
)

var debVersion = regexp.MustCompile(`^([0-9]+:)?[0-9][A-Za-z0-9.+~\-]*$`)

func validVersion(s string) bool { return debVersion.MatchString(s) }

// CompareVersions follows dpkg's epoch, upstream and revision ordering, including '~' before
// the empty string. It needs no host dpkg, including on Windows and macOS.
func CompareVersions(a, b string) int {
	split := func(s string) (epoch, upstream, revision string) {
		epoch = "0"
		if i := strings.IndexByte(s, ':'); i >= 0 {
			epoch, s = s[:i], s[i+1:]
		}
		upstream, revision = s, "0"
		if i := strings.LastIndexByte(s, '-'); i >= 0 {
			upstream, revision = s[:i], s[i+1:]
		}
		return
	}
	ae, au, ar := split(a)
	be, bu, br := split(b)
	if c := numberCompare(ae, be); c != 0 {
		return c
	}
	if c := partCompare(au, bu); c != 0 {
		return c
	}
	return partCompare(ar, br)
}

func numberCompare(a, b string) int {
	a, b = strings.TrimLeft(a, "0"), strings.TrimLeft(b, "0")
	if c := cmp.Compare(len(a), len(b)); c != 0 {
		return c
	}
	return strings.Compare(a, b)
}

func digit(b byte) bool { return b >= '0' && b <= '9' }

func order(s string) int {
	if s == "" || digit(s[0]) {
		return 0
	}
	b := s[0]
	if b == '~' {
		return -1
	}
	if b >= 'A' && b <= 'Z' || b >= 'a' && b <= 'z' {
		return int(b)
	}
	return int(b) + 256
}

func partCompare(a, b string) int {
	for a != "" || b != "" {
		for a != "" && !digit(a[0]) || b != "" && !digit(b[0]) {
			if c := cmp.Compare(order(a), order(b)); c != 0 {
				return c
			}
			if a != "" {
				a = a[1:]
			}
			if b != "" {
				b = b[1:]
			}
		}
		ai, bi := 0, 0
		for ai < len(a) && digit(a[ai]) {
			ai++
		}
		for bi < len(b) && digit(b[bi]) {
			bi++
		}
		if c := numberCompare(a[:ai], b[:bi]); c != 0 {
			return c
		}
		a, b = a[ai:], b[bi:]
	}
	return 0
}
