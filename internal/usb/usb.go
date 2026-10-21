// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-20

// Package usb describes USB devices on the host bus. Listing reads descriptors only: it never opens,
// claims or writes to a device. Protocol clients (adb, fastboot, EDL) build on later interfaces.
package usb

import (
	"context"
	"fmt"
	"strings"
)

// Interface is the class triple of one interface alternate setting.
type Interface struct {
	Class    uint8 `json:"class"`
	SubClass uint8 `json:"subClass"`
	Protocol uint8 `json:"protocol"`
}

// DeviceInfo is one device as seen on the bus, read from its descriptors.
type DeviceInfo struct {
	VendorID   uint16      `json:"vendorId"`
	ProductID  uint16      `json:"productId"`
	Bus        int         `json:"bus"`
	Address    int         `json:"address"`
	Path       []int       `json:"path"`
	Interfaces []Interface `json:"interfaces"`
}

// ID returns the device's VID:PID in the usual lower-case hex form, e.g. "18d1:d001".
func (d DeviceInfo) ID() string {
	return fmt.Sprintf("%04x:%04x", d.VendorID, d.ProductID)
}

// Location names the physical port chain, e.g. "1-2.3"; it stays stable while the device changes
// mode on the same port, unlike the bus address.
func (d DeviceInfo) Location() string {
	if len(d.Path) == 0 {
		return fmt.Sprintf("%d", d.Bus)
	}
	ports := make([]string, len(d.Path))
	for i, p := range d.Path {
		ports[i] = fmt.Sprintf("%d", p)
	}
	return fmt.Sprintf("%d-%s", d.Bus, strings.Join(ports, "."))
}

// HasInterface reports whether any interface setting matches the class triple.
func (d DeviceInfo) HasInterface(want Interface) bool {
	for _, i := range d.Interfaces {
		if i == want {
			return true
		}
	}
	return false
}

// Bus lists the devices currently attached to the host.
type Bus interface {
	List(ctx context.Context) ([]DeviceInfo, error)
	Close() error
}
