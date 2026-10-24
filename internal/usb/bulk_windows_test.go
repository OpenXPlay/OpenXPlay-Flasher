// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only

package usb

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// WinUSB writes these structures directly. A Go padding change must not silently
// change endpoint numbers or the maximum packet size on Windows.
func TestWinUSBDescriptorABI(t *testing.T) {
	var desc winUSBDescriptor
	if got := unsafe.Sizeof(desc); got != 9 {
		t.Errorf("USB_INTERFACE_DESCRIPTOR size = %d, want 9", got)
	}
	if unsafe.Offsetof(desc.Class) != 5 || unsafe.Offsetof(desc.Protocol) != 7 {
		t.Errorf("USB interface class/protocol offsets = %d/%d, want 5/7", unsafe.Offsetof(desc.Class), unsafe.Offsetof(desc.Protocol))
	}
	var pipe winUSBPipe
	if got := unsafe.Sizeof(pipe); got != 12 {
		t.Errorf("WINUSB_PIPE_INFORMATION size = %d, want 12", got)
	}
	if unsafe.Offsetof(pipe.ID) != 4 || unsafe.Offsetof(pipe.Packet) != 6 || unsafe.Offsetof(pipe.Interval) != 8 {
		t.Errorf("WinUSB pipe offsets = %d/%d/%d, want 4/6/8", unsafe.Offsetof(pipe.ID), unsafe.Offsetof(pipe.Packet), unsafe.Offsetof(pipe.Interval))
	}
}

func TestWinUSBErrorClassification(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   error
		want error
	}{
		{"success", nil, nil},
		{"in use", windows.ERROR_SHARING_VIOLATION, ErrBusy},
		{"access denied", windows.ERROR_ACCESS_DENIED, ErrBusy},
		{"removed", windows.ERROR_DEVICE_NOT_CONNECTED, ErrNotFound},
		{"absent", windows.ERROR_NO_SUCH_DEVICE, ErrNotFound},
		{"missing path", windows.ERROR_FILE_NOT_FOUND, ErrNotFound},
		{"cancelled", windows.ERROR_OPERATION_ABORTED, windows.ERROR_OPERATION_ABORTED},
		{"stall", windows.ERROR_GEN_FAILURE, windows.ERROR_GEN_FAILURE},
		{"wrapped removal", fmt.Errorf("completion: %w", windows.ERROR_DEVICE_NOT_CONNECTED), ErrNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := winUSBError(tc.in); !errors.Is(got, tc.want) {
				t.Fatalf("winUSBError(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// Exercise the BOOL/last-error wrapper using only a private kernel event. These
// tests must never open or submit transfers to an attached USB device.
func TestWinUSBCallBOOLResult(t *testing.T) {
	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(event)
	setEvent := windows.NewLazySystemDLL("kernel32.dll").NewProc("SetEvent")
	if err := winUSBCall(setEvent, uintptr(event)); err != nil {
		t.Fatalf("SetEvent valid handle: %v", err)
	}
	if state, err := windows.WaitForSingleObject(event, 0); err != nil || state != windows.WAIT_OBJECT_0 {
		t.Fatalf("event state = %d, %v", state, err)
	}
	if err := winUSBCall(setEvent, 0); !errors.Is(err, windows.ERROR_INVALID_HANDLE) {
		t.Fatalf("SetEvent invalid handle = %v, want ERROR_INVALID_HANDLE", err)
	}
}

func TestWinBulkCancelledAndClosed(t *testing.T) {
	b := &winBulk{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, transfer := range []func(context.Context, []byte) (int, error){b.Read, b.Write} {
		if n, err := transfer(ctx, []byte{1}); n != 0 || !errors.Is(err, context.Canceled) {
			t.Errorf("cancelled transfer = %d, %v", n, err)
		}
		if n, err := transfer(context.Background(), []byte{1}); n != 0 || !errors.Is(err, ErrNotFound) {
			t.Errorf("closed transfer = %d, %v", n, err)
		}
	}
}

func TestWinBulkCloseReleasesFileOnce(t *testing.T) {
	// An event provides an owned, harmless Windows handle. With no interface
	// initialized, Close must release that handle and remain safe to call twice.
	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	b := &winBulk{file: event}
	defer b.Close()
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if b.file != 0 {
		t.Errorf("Close kept file handle %v", b.file)
	}
	if _, err := windows.WaitForSingleObject(event, 0); !errors.Is(err, windows.ERROR_INVALID_HANDLE) {
		t.Errorf("closed handle remains valid: %v", err)
	}
	if err := b.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

func TestOpenWinUSBRejectsEmbeddedNUL(t *testing.T) {
	if b, err := openWinUSB("invalid\x00device", Interface{}); err == nil || b != nil {
		t.Fatalf("embedded-NUL device path = %v, %v", b, err)
	}
}

func TestDecodeUSBString(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
		want string
	}{
		{"case-sensitive serial", []byte{18, 3, 'f', 0, '9', 0, '8', 0, '1', 0, '6', 0, 'a', 0, 'c', 0, '0', 0}, "f9816ac0"},
		{"non-ASCII UTF-16", []byte{6, 3, 0x2d, 0x4e, 0x87, 0x65}, "中文"},
		{"surrogate pair", []byte{6, 3, 0x3d, 0xd8, 0x00, 0xde}, "😀"},
		{"empty string", []byte{2, 3}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, err := decodeUSBString(tc.data); err != nil || got != tc.want {
				t.Fatalf("decodeUSBString(%x) = %q, %v; want %q", tc.data, got, err, tc.want)
			}
		})
	}
	for _, invalid := range [][]byte{
		nil,
		{2},
		{4, 1, 'x', 0}, // not a string descriptor
		{6, 3, 'x', 0}, // truncated UTF-16
		{2, 3, 'x', 0}, // unexpected bytes after the declared length
		{3, 3, 'x'},    // odd UTF-16 byte length
	} {
		if got, err := decodeUSBString(invalid); err == nil {
			t.Errorf("decodeUSBString accepted %x as %q", invalid, got)
		}
	}
}

func TestWinBulkSerialNumberAfterClose(t *testing.T) {
	b := &winBulk{}
	if serial, err := b.SerialNumber(); serial != "" || !errors.Is(err, ErrNotFound) {
		t.Fatalf("SerialNumber on closed interface = %q, %v", serial, err)
	}
}
