// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package profile

import (
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"
)

type Archive struct {
	File   string `json:"file"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type Member struct {
	Archive string `json:"archive"`
	Member  string `json:"member,omitempty"`
}

type BaseFile struct {
	Archive string `json:"archive"`
	Member  string `json:"member,omitempty"`
	Size    int64  `json:"size"`
	SHA256  string `json:"sha256"`
}

type BaseFastboot struct {
	StockProduct string `json:"stockProduct"`
	Product      string `json:"product"`
	MaxPiece     int64  `json:"maxPiece"`
}

type Stock struct {
	Properties    map[string]string `json:"properties"`
	ReleasePrefix string            `json:"releasePrefix"`
}

type Linux struct {
	KernelRelease   string `json:"kernelRelease"`
	DeviceTreeModel string `json:"deviceTreeModel"`
	DebianVersion   string `json:"debianVersion"`
	VendorID        string `json:"vendorId"`
	ProductID       string `json:"productId"`
}

// Base contains pins and identity facts, never executable commands or a write recipe.
type Base struct {
	Release    string              `json:"release"`
	Source     string              `json:"source"`
	Archives   map[string]Archive  `json:"archives"`
	Files      map[string]BaseFile `json:"files"`
	NeverWrite map[string]Member   `json:"neverWrite"`
	// Protected lists the sizes a dump of each partition may have, ascending: lk1st's oem dump
	// returns the partition at the size the GPT in place gives it (stock and OpenStick differ).
	Protected map[string][]int64 `json:"protected"`
	Fastboot  BaseFastboot       `json:"fastboot"`
	Stock     Stock              `json:"stock"`
	Linux     Linux              `json:"linux"`
}

var baseToken = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
var baseHash = regexp.MustCompile(`^[0-9a-f]{64}$`)

func cleanMember(s string) bool {
	return s != "" && !path.IsAbs(s) && path.Clean(s) == s && !strings.ContainsAny(s, "\\\x00\r\n:") && !strings.HasPrefix(s, "../") && s != ".." && s != "."
}

func (b Base) Validate() error {
	bad := func(s string) error { return fmt.Errorf("invalid %s", s) }
	u, err := url.Parse(b.Source)
	if !baseToken.MatchString(b.Release) || err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return bad("release or source")
	}
	if len(b.Archives) != 3 {
		return bad("archive roles")
	}
	for _, role := range []string{"base-generic", "rootfs-zip", "boot"} {
		a := b.Archives[role]
		if !cleanMember(a.File) || path.Base(a.File) != a.File || a.Size <= 0 || a.Size > 2<<30 || !baseHash.MatchString(a.SHA256) {
			return bad("archive " + role)
		}
	}
	for _, id := range []string{"gpt", "hyp", "rpm", "sbl1", "tz", "aboot", "cdt", "rootfs", "boot"} {
		f := b.Files[id]
		if _, ok := b.Archives[f.Archive]; !ok || f.Size <= 0 || f.Size > 2<<30 || !baseHash.MatchString(f.SHA256) || f.Member != "" && !cleanMember(f.Member) {
			return bad("file " + id)
		}
		if id == "boot" {
			a := b.Archives["boot"]
			if f.Archive != "boot" || f.Member != "" || f.Size != a.Size || f.SHA256 != a.SHA256 {
				return bad("board boot pin")
			}
		} else if f.Member == "" {
			return bad("member " + id)
		}
	}
	if len(b.Files) != 9 || len(b.NeverWrite) != 4 || len(b.Protected) != 4 {
		return bad("files or protected partitions")
	}
	seen := map[Member]bool{}
	for _, name := range []string{"fsc", "fsg", "modemst1", "modemst2"} {
		m := b.NeverWrite[name]
		if m.Archive != "base-generic" || !cleanMember(m.Member) || path.Base(m.Member) != name+".bin" || !validSizes(b.Protected[name]) {
			return bad("protected partition " + name)
		}
		seen[m] = true
	}
	for _, f := range b.Files {
		if seen[Member{f.Archive, f.Member}] {
			return bad("writable author modem member")
		}
	}
	if b.Fastboot.Product == "" || b.Fastboot.StockProduct == "" || b.Fastboot.Product == b.Fastboot.StockProduct || b.Fastboot.MaxPiece <= 0 || b.Fastboot.MaxPiece > 200<<20 {
		return bad("fastboot identity or limit")
	}
	for _, key := range []string{"ro.product.model", "ro.product.name", "ro.product.device"} {
		if b.Stock.Properties[key] == "" {
			return bad("stock property " + key)
		}
	}
	if len(b.Stock.Properties) != 3 || b.Stock.ReleasePrefix == "" {
		return bad("stock identity")
	}
	if b.Linux.KernelRelease == "" || b.Linux.DeviceTreeModel == "" || b.Linux.DebianVersion == "" {
		return bad("Linux identity")
	}
	_, _, err = (USBMatch{VendorID: b.Linux.VendorID, ProductID: b.Linux.ProductID}).IDs()
	return err
}

// validSizes: at least one size, ascending, each above zero and at most 16 MiB.
func validSizes(sizes []int64) bool {
	if len(sizes) == 0 {
		return false
	}
	for i, n := range sizes {
		if n <= 0 || n > 16<<20 || i > 0 && n <= sizes[i-1] {
			return false
		}
	}
	return true
}

// MaxProtected is the largest dump size the profile allows for a protected partition.
func (b *Base) MaxProtected(name string) int64 {
	sizes := b.Protected[name]
	if len(sizes) == 0 {
		return 0
	}
	return sizes[len(sizes)-1]
}

// DisplayName uses the pinned upstream release rather than the distribution's version.
func (b Base) DisplayName() string { return "OpenStick " + strings.TrimPrefix(b.Release, "openstick-") }
