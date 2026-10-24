// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only

package usb

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"slices"
	"sync"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

var (
	winusbDLL          = windows.NewLazySystemDLL("winusb.dll")
	winusbInitialize   = winusbDLL.NewProc("WinUsb_Initialize")
	winusbFree         = winusbDLL.NewProc("WinUsb_Free")
	winusbQuerySetting = winusbDLL.NewProc("WinUsb_QueryInterfaceSettings")
	winusbQueryPipe    = winusbDLL.NewProc("WinUsb_QueryPipe")
	winusbReadPipe     = winusbDLL.NewProc("WinUsb_ReadPipe")
	winusbWritePipe    = winusbDLL.NewProc("WinUsb_WritePipe")
	winusbSetPolicy    = winusbDLL.NewProc("WinUsb_SetPipePolicy")
	winusbDescriptor   = winusbDLL.NewProc("WinUsb_GetDescriptor")
)

// These layouts match USB_INTERFACE_DESCRIPTOR and WINUSB_PIPE_INFORMATION in the Windows SDK.
type winUSBDescriptor struct {
	Length, DescriptorType, Number, Alternate, Endpoints, Class, SubClass, Protocol, String uint8
}

type winUSBPipe struct {
	Type     uint32
	ID       uint8
	_        uint8
	Packet   uint16
	Interval uint8
	_        [3]byte
}

// winBulk owns an overlapped file handle and its WinUSB interface. Read and Write may run
// concurrently (adb has a background reader); Close waits for both to release their buffers.
type winBulk struct {
	mu     sync.RWMutex
	file   windows.Handle
	intf   uintptr
	in     uint8
	out    uint8
	packet int
}

func TransfersSupported() bool { return true }

// OpenBulk opens only the requested USB port and interface. Listing never opens a device.
// The interface must have a WinUSB driver, including Microsoft's built-in ADB driver.
func OpenBulk(ctx context.Context, info DeviceInfo, want Interface) (Bulk, error) {
	nodes, err := setupAPINodes(ctx)
	if err != nil {
		return nil, err
	}
	for _, current := range devicesFromNodes(nodes) {
		if current.ID() == info.ID() && current.Bus == info.Bus && slices.Equal(current.Path, info.Path) &&
			info.Serial != "" && !current.MatchesSerial(info.Serial) {
			return nil, fmt.Errorf("%w: USB device at %s changed since scanning", ErrNotFound, info.Location())
		}
	}
	var paths []string
	matchedNodes := 0
	for _, n := range nodes {
		vendor, product, _, ok := parseInstanceID(n.InstanceID)
		bus, ports, located := parseLocation(n.LocationPaths)
		if !ok || vendor != info.VendorID || product != info.ProductID || !located ||
			bus != info.Bus || !slices.Equal(ports, info.Path) {
			continue
		}
		if c, ok := parseClass(n.CompatibleIDs); !ok || c != want {
			continue
		}
		matchedNodes++
		if matchedNodes > 1 {
			return nil, fmt.Errorf("usb: multiple %02x/%02x/%02x interfaces at %s; cannot select one safely",
				want.Class, want.SubClass, want.Protocol, info.Location())
		}
		p, err := winUSBPaths(n.InstanceID)
		if err != nil {
			return nil, fmt.Errorf("usb: %s at %s needs a WinUSB driver for %02x/%02x/%02x: %w",
				info.ID(), info.Location(), want.Class, want.SubClass, want.Protocol, err)
		}
		paths = append(paths, p...)
	}
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		b, err := openWinUSB(path, want)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		return b, err
	}
	return nil, fmt.Errorf("%w: %s at %s has no WinUSB %02x/%02x/%02x interface",
		ErrNotFound, info.ID(), info.Location(), want.Class, want.SubClass, want.Protocol)
}

// The driver registers its interface GUID(s) in the hardware key. Query each GUID for this
// exact device instance: a class-wide first match could open another phone or dongle.
func winUSBPaths(instance string) ([]string, error) {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE,
		`SYSTEM\CurrentControlSet\Enum\`+instance+`\Device Parameters`, registry.QUERY_VALUE)
	if err != nil {
		return nil, err
	}
	defer key.Close()
	guids, _, _ := key.GetStringsValue("DeviceInterfaceGUIDs")
	if guid, _, err := key.GetStringValue("DeviceInterfaceGUID"); err == nil {
		guids = append(guids, guid)
	}
	var paths []string
	for _, text := range guids {
		guid, err := windows.GUIDFromString(text)
		if err != nil {
			continue
		}
		p, err := windows.CM_Get_Device_Interface_List(instance, &guid, windows.CM_GET_DEVICE_INTERFACE_LIST_PRESENT)
		if err != nil {
			continue
		}
		for _, path := range p {
			if !slices.Contains(paths, path) {
				paths = append(paths, path)
			}
		}
	}
	if len(paths) == 0 {
		return nil, errors.New("no registered device interface; install the ADB/fastboot WinUSB driver")
	}
	return paths, nil
}

func openWinUSB(path string, want Interface) (_ *winBulk, err error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	file, err := windows.CreateFile(p, windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_OVERLAPPED, 0)
	if err != nil {
		return nil, winUSBError(err)
	}
	b := &winBulk{file: file}
	defer func() {
		if err != nil {
			b.Close()
		}
	}()
	if err = winUSBCall(winusbInitialize, uintptr(file), uintptr(unsafe.Pointer(&b.intf))); err != nil {
		return nil, winUSBError(err)
	}
	var desc winUSBDescriptor
	if err = winUSBCall(winusbQuerySetting, b.intf, 0, uintptr(unsafe.Pointer(&desc))); err != nil {
		return nil, winUSBError(err)
	}
	if (Interface{desc.Class, desc.SubClass, desc.Protocol}) != want {
		return nil, ErrNotFound
	}
	for i := uint8(0); i < desc.Endpoints; i++ {
		var pipe winUSBPipe
		if err = winUSBCall(winusbQueryPipe, b.intf, 0, uintptr(i), uintptr(unsafe.Pointer(&pipe))); err != nil {
			return nil, winUSBError(err)
		}
		if pipe.Type != 2 { // UsbdPipeTypeBulk
			continue
		}
		if pipe.ID&0x80 != 0 {
			b.in = pipe.ID
		} else {
			b.out, b.packet = pipe.ID, int(pipe.Packet)
		}
	}
	if b.in == 0 || b.out == 0 || b.packet == 0 {
		return nil, ErrNotFound
	}
	// adb reads the 24-byte header separately from the payload. Keep excess data queued and
	// return on a short packet, so a header read cannot discard the following payload.
	for _, policy := range []struct{ pipe, kind, value uint8 }{
		{b.in, 4, 0},  // IGNORE_SHORT_PACKETS = false
		{b.in, 5, 1},  // ALLOW_PARTIAL_READS = true
		{b.in, 6, 0},  // AUTO_FLUSH = false
		{b.out, 1, 0}, // SHORT_PACKET_TERMINATE = false: protocol clients send ZLP explicitly
	} {
		value := policy.value
		if err = winUSBCall(winusbSetPolicy, b.intf, uintptr(policy.pipe), uintptr(policy.kind),
			1, uintptr(unsafe.Pointer(&value))); err != nil {
			return nil, winUSBError(err)
		}
	}
	return b, nil
}

//go:uintptrescapes
func winUSBCall(proc *windows.LazyProc, args ...uintptr) error {
	result, _, err := proc.Call(args...)
	if result != 0 {
		return nil
	}
	if err == windows.ERROR_SUCCESS {
		return windows.ERROR_GEN_FAILURE
	}
	return err
}

func winUSBError(err error) error {
	switch {
	case errors.Is(err, windows.ERROR_SHARING_VIOLATION), errors.Is(err, windows.ERROR_ACCESS_DENIED):
		return fmt.Errorf("%w (%v)", ErrBusy, err)
	case errors.Is(err, windows.ERROR_FILE_NOT_FOUND), errors.Is(err, windows.ERROR_DEVICE_NOT_CONNECTED),
		errors.Is(err, windows.ERROR_NO_SUCH_DEVICE):
		return fmt.Errorf("%w (%v)", ErrNotFound, err)
	}
	return err
}

func (b *winBulk) Read(ctx context.Context, p []byte) (int, error) {
	return b.transfer(ctx, p, b.in, winusbReadPipe)
}

func (b *winBulk) Write(ctx context.Context, p []byte) (int, error) {
	return b.transfer(ctx, p, b.out, winusbWritePipe)
}

func (b *winBulk) transfer(ctx context.Context, p []byte, pipe uint8, proc *windows.LazyProc) (int, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if b.intf == 0 {
		return 0, ErrNotFound
	}
	if uint64(len(p)) > uint64(^uint32(0)) {
		return 0, errors.New("usb: transfer exceeds Windows buffer limit")
	}
	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return 0, err
	}
	defer windows.CloseHandle(event)
	overlap := &windows.Overlapped{HEvent: event}
	var buffer *byte
	if len(p) > 0 {
		buffer = &p[0]
	}
	var done uint32
	err = winUSBCall(proc, b.intf, uintptr(pipe), uintptr(unsafe.Pointer(buffer)), uintptr(len(p)),
		uintptr(unsafe.Pointer(&done)), uintptr(unsafe.Pointer(overlap)))
	if errors.Is(err, windows.ERROR_IO_PENDING) {
		// Cancel only this operation. CancelIoEx returns before completion; always drain the
		// overlapped result before releasing its event, buffer or OVERLAPPED storage.
		cancelled := make(chan struct{})
		stop := context.AfterFunc(ctx, func() {
			windows.CancelIoEx(b.file, overlap)
			close(cancelled)
		})
		err = windows.GetOverlappedResult(b.file, overlap, &done, true)
		if !stop() {
			<-cancelled
		}
	}
	runtime.KeepAlive(p)
	runtime.KeepAlive(overlap)
	if err != nil && ctx.Err() != nil {
		return int(done), ctx.Err()
	}
	return int(done), winUSBError(err)
}

func (b *winBulk) MaxPacketSize() int { return b.packet }

// Windows may uppercase the serial in a PnP instance ID. Authentication and stock-firmware
// checks need the actual USB string, including its case, rather than that display identifier.
func (b *winBulk) SerialNumber() (string, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.intf == 0 {
		return "", ErrNotFound
	}
	device, err := b.descriptor(1, 0, 0)
	if err != nil {
		return "", err
	}
	if len(device) < 18 || device[16] == 0 {
		return "", errors.New("usb: no serial string descriptor")
	}
	languages, err := b.descriptor(3, 0, 0)
	if err != nil {
		return "", err
	}
	if len(languages) < 4 {
		return "", errors.New("usb: no USB string languages")
	}
	language := uint16(languages[2]) | uint16(languages[3])<<8
	serial, err := b.descriptor(3, device[16], language)
	if err != nil {
		return "", err
	}
	return decodeUSBString(serial)
}

func (b *winBulk) descriptor(kind, index uint8, language uint16) ([]byte, error) {
	var buffer [255]byte
	var length uint32
	err := winUSBCall(winusbDescriptor, b.intf, uintptr(kind), uintptr(index), uintptr(language),
		uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)), uintptr(unsafe.Pointer(&length)))
	if err != nil {
		return nil, winUSBError(err)
	}
	if length < 2 || length > uint32(len(buffer)) || buffer[1] != kind ||
		buffer[0] < 2 || uint32(buffer[0]) > length {
		return nil, errors.New("usb: invalid USB descriptor")
	}
	return buffer[:buffer[0]], nil
}

func decodeUSBString(descriptor []byte) (string, error) {
	if len(descriptor) < 2 || descriptor[1] != 3 || int(descriptor[0]) != len(descriptor) || len(descriptor)%2 != 0 {
		return "", errors.New("usb: invalid USB string descriptor")
	}
	chars := make([]uint16, (len(descriptor)-2)/2)
	for i := range chars {
		chars[i] = uint16(descriptor[2+i*2]) | uint16(descriptor[3+i*2])<<8
	}
	return string(utf16.Decode(chars)), nil
}

func (b *winBulk) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.intf != 0 {
		winusbFree.Call(b.intf)
		b.intf = 0
	}
	if b.file != 0 && b.file != windows.InvalidHandle {
		err := windows.CloseHandle(b.file)
		b.file = 0
		return err
	}
	return nil
}
