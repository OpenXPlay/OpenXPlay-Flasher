// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

//go:build cgo && !windows

package usb

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/google/gousb"
)

// libusbBulk is an interface claimed through libusb, in a libusb context of its own.
type libusbBulk struct {
	mu     sync.Mutex
	ctx    *gousb.Context
	dev    *gousb.Device
	cfg    *gousb.Config
	intf   *gousb.Interface
	in     *gousb.InEndpoint
	out    *gousb.OutEndpoint
	packet int
}

// OpenBulk opens the device at dev's port (same IDs, bus and port chain) and claims the first
// setting of its active configuration that has the class triple want and a bulk IN/OUT pair.
// The active configuration is never changed.
func OpenBulk(ctx context.Context, dev DeviceInfo, want Interface) (Bulk, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	b := &libusbBulk{ctx: gousb.NewContext()}
	if err := b.open(dev, want); err != nil {
		b.Close()
		return nil, err
	}
	return b, nil
}

func (b *libusbBulk) open(info DeviceInfo, want Interface) error {
	devs, err := b.ctx.OpenDevices(func(desc *gousb.DeviceDesc) bool {
		return uint16(desc.Vendor) == info.VendorID && uint16(desc.Product) == info.ProductID &&
			desc.Bus == info.Bus && slices.Equal(desc.Path, info.Path) &&
			(len(info.Path) > 0 || desc.Address == info.Address)
	})
	for _, d := range devs[min(1, len(devs)):] {
		d.Close()
	}
	if len(devs) == 0 {
		if err != nil {
			return fmt.Errorf("usb: open %s at %s: %w", info.ID(), info.Location(), libusbErr(err, false))
		}
		return fmt.Errorf("%w: %s at %s", ErrNotFound, info.ID(), info.Location())
	}
	b.dev = devs[0]
	num, err := b.dev.ActiveConfigNum()
	if err != nil {
		return fmt.Errorf("usb: active configuration of %s: %w", info.ID(), libusbErr(err, false))
	}
	setting, in, out, ok := findBulk(b.dev.Desc.Configs[num], want)
	if !ok {
		return fmt.Errorf("%w: %s has no %02x/%02x/%02x bulk interface", ErrNotFound, info.ID(),
			want.Class, want.SubClass, want.Protocol)
	}
	if b.cfg, err = b.dev.Config(num); err != nil {
		return fmt.Errorf("usb: configuration %d of %s: %w", num, info.ID(), libusbErr(err, true))
	}
	if b.intf, err = b.cfg.Interface(setting.Number, setting.Alternate); err != nil {
		return fmt.Errorf("usb: claim interface %d of %s: %w", setting.Number, info.ID(), libusbErr(err, true))
	}
	if b.in, err = b.intf.InEndpoint(in.Number); err != nil {
		return err
	}
	if b.out, err = b.intf.OutEndpoint(out.Number); err != nil {
		return err
	}
	b.packet = out.MaxPacketSize
	return nil
}

// findBulk returns the first setting matching want with a bulk IN and a bulk OUT endpoint.
func findBulk(cfg gousb.ConfigDesc, want Interface) (gousb.InterfaceSetting, gousb.EndpointDesc, gousb.EndpointDesc, bool) {
	for _, intf := range cfg.Interfaces {
		for _, alt := range intf.AltSettings {
			if (Interface{Class: uint8(alt.Class), SubClass: uint8(alt.SubClass), Protocol: uint8(alt.Protocol)}) != want {
				continue
			}
			var in, out *gousb.EndpointDesc
			for _, addr := range slices.Sorted(maps.Keys(alt.Endpoints)) {
				ep := alt.Endpoints[addr]
				if ep.TransferType != gousb.TransferTypeBulk {
					continue
				}
				if ep.Direction == gousb.EndpointDirectionIn && in == nil {
					in = &ep
				} else if ep.Direction == gousb.EndpointDirectionOut && out == nil {
					out = &ep
				}
			}
			if in != nil && out != nil {
				return alt, *in, *out, true
			}
		}
	}
	return gousb.InterfaceSetting{}, gousb.EndpointDesc{}, gousb.EndpointDesc{}, false
}

// libusbErr maps libusb's busy and access errors to ErrBusy when claiming (macOS reports an
// interface another program has open as an access error), and a missing device to ErrNotFound.
// gousb wraps some errors as text only, so the codes are matched in the message too.
func libusbErr(err error, claiming bool) error {
	has := func(code gousb.Error) bool {
		var e gousb.Error
		return (errors.As(err, &e) && e == code) || strings.Contains(err.Error(), code.Error())
	}
	switch {
	case claiming && (has(gousb.ErrorBusy) || has(gousb.ErrorAccess)):
		return fmt.Errorf("%w (%v)", ErrBusy, err)
	case has(gousb.ErrorNoDevice) || has(gousb.ErrorNotFound):
		return fmt.Errorf("%w (%v)", ErrNotFound, err)
	}
	return err
}

func (b *libusbBulk) Read(ctx context.Context, p []byte) (int, error) {
	n, err := b.in.ReadContext(ctx, p)
	return n, transferErr(ctx, err)
}

func (b *libusbBulk) Write(ctx context.Context, p []byte) (int, error) {
	n, err := b.out.WriteContext(ctx, p)
	return n, transferErr(ctx, err)
}

// transferErr reports a cancelled transfer as the context's error.
func transferErr(ctx context.Context, err error) error {
	switch {
	case err == nil:
		return nil
	case ctx.Err() != nil:
		return ctx.Err()
	case errors.Is(err, gousb.TransferNoDevice):
		return fmt.Errorf("%w (%v)", ErrNotFound, err)
	}
	return err
}

func (b *libusbBulk) MaxPacketSize() int { return b.packet }

// Close releases the interface, the configuration, the device and the context, in that order;
// the caller has no transfer running.
func (b *libusbBulk) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.ctx == nil {
		return nil
	}
	if b.intf != nil {
		b.intf.Close()
	}
	var errs []error
	if b.cfg != nil {
		errs = append(errs, b.cfg.Close())
	}
	if b.dev != nil {
		errs = append(errs, b.dev.Close())
	}
	errs = append(errs, b.ctx.Close())
	b.ctx, b.dev, b.cfg, b.intf = nil, nil, nil, nil
	return errors.Join(errs...)
}
