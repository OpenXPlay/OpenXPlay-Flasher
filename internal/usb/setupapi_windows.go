// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-20

package usb

import (
	"context"
	"errors"

	"golang.org/x/sys/windows"
)

// SetupAPIBus lists USB devices on Windows through SetupAPI, in pure Go: it reads the device
// nodes Windows already has and opens no device, so it needs no driver and no cgo. Data transfers
// go through WinUSB once a device's interface is bound to it.
type SetupAPIBus struct{}

// NewHostBus returns the bus of this computer.
func NewHostBus() Bus { return SetupAPIBus{} }

func (SetupAPIBus) List(ctx context.Context) ([]DeviceInfo, error) {
	nodes, err := setupAPINodes(ctx)
	if err != nil {
		return nil, err
	}
	return devicesFromNodes(nodes), nil
}

func (SetupAPIBus) Close() error { return nil }

func setupAPINodes(ctx context.Context) ([]winNode, error) {
	set, err := windows.SetupDiGetClassDevsEx(nil, "USB", 0, windows.DIGCF_ALLCLASSES|windows.DIGCF_PRESENT, 0, "")
	if err != nil {
		return nil, err
	}
	defer set.Close()
	var nodes []winNode
	for i := 0; ; i++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		data, err := set.EnumDeviceInfo(i)
		if errors.Is(err, windows.ERROR_NO_MORE_ITEMS) {
			return nodes, nil
		}
		if err != nil {
			return nil, err
		}
		id, err := set.DeviceInstanceID(data)
		if err != nil {
			continue
		}
		n := winNode{InstanceID: id}
		if v, err := set.DeviceRegistryProperty(data, windows.SPDRP_COMPATIBLEIDS); err == nil {
			n.CompatibleIDs = stringList(v)
		}
		if v, err := set.DeviceRegistryProperty(data, windows.SPDRP_LOCATION_PATHS); err == nil {
			n.LocationPaths = stringList(v)
		}
		if v, err := set.DeviceRegistryProperty(data, windows.SPDRP_ADDRESS); err == nil {
			if a, ok := v.(uint32); ok {
				n.Address = a
			}
		}
		nodes = append(nodes, n)
	}
}

// stringList normalises a registry property to a string list (REG_MULTI_SZ or REG_SZ).
func stringList(v any) []string {
	switch s := v.(type) {
	case []string:
		return s
	case string:
		return []string{s}
	}
	return nil
}
