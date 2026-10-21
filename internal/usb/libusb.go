// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-20

//go:build cgo

package usb

import (
	"context"
	"maps"
	"slices"
	"sync"

	"github.com/google/gousb"
)

// LibusbBus lists devices through libusb 1.0 (gousb, cgo, linked dynamically).
type LibusbBus struct {
	mu  sync.Mutex
	ctx *gousb.Context
}

// NewLibusbBus opens a libusb context. Close releases it.
func NewLibusbBus() *LibusbBus {
	return &LibusbBus{ctx: gousb.NewContext()}
}

func (b *LibusbBus) List(ctx context.Context) ([]DeviceInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []DeviceInfo
	// The opener returns false for every device, so libusb only reads the cached descriptors and
	// opens nothing.
	_, err := b.ctx.OpenDevices(func(desc *gousb.DeviceDesc) bool {
		out = append(out, fromDesc(desc))
		return false
	})
	return out, err
}

func (b *LibusbBus) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.ctx.Close()
}

func fromDesc(desc *gousb.DeviceDesc) DeviceInfo {
	info := DeviceInfo{
		VendorID:  uint16(desc.Vendor),
		ProductID: uint16(desc.Product),
		Bus:       desc.Bus,
		Address:   desc.Address,
		Path:      slices.Clone(desc.Path),
	}
	for _, n := range slices.Sorted(maps.Keys(desc.Configs)) {
		for _, intf := range desc.Configs[n].Interfaces {
			for _, alt := range intf.AltSettings {
				i := Interface{Class: uint8(alt.Class), SubClass: uint8(alt.SubClass), Protocol: uint8(alt.Protocol)}
				if !slices.Contains(info.Interfaces, i) {
					info.Interfaces = append(info.Interfaces, i)
				}
			}
		}
	}
	return info
}
