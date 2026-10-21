// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package adb

import (
	"context"
	"errors"
	"fmt"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/usb"
)

// AutoConnector reaches a device through the adb server when the server lists it, and directly
// over USB when no server answers or the server does not list the device.
type AutoConnector struct {
	Server ServerConnector
	USB    USBConnector
}

// NewConnector returns the connector the app uses: the adb server at DefaultServerAddr, else
// native USB with the key in <user config dir>/OpenXPlay Flasher/adbkey.
func NewConnector() Connector { return &AutoConnector{} }

func (a *AutoConnector) Connect(ctx context.Context, dev usb.DeviceInfo) (Device, error) {
	d, serr := a.Server.Connect(ctx, dev)
	if serr == nil {
		return d, nil
	}
	if !errors.Is(serr, errNoServer) && !errors.Is(serr, ErrNoDevice) {
		return nil, serr
	}
	d, uerr := a.USB.Connect(ctx, dev)
	switch {
	case uerr == nil:
		return d, nil
	case !errors.Is(uerr, usb.ErrTransfersUnsupported):
		return nil, uerr
	case errors.Is(serr, ErrNoDevice):
		// The server runs but does not list the device, and there is no other way to it.
		return nil, serr
	}
	return nil, fmt.Errorf("%v, and %w", serr, uerr)
}
