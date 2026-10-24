// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-20

package usb

import (
	"hash/fnv"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// The Windows backend lists USB devices from the device nodes SetupAPI reports under the "USB"
// enumerator. Turning those nodes into DeviceInfo is plain string work, kept here without build
// tags so it is tested on every platform.

// winNode is one USB device node as SetupAPI reports it.
type winNode struct {
	InstanceID    string   // USB\VID_18D1&PID_D001\0123456789, or ...&MI_01\... for an interface
	CompatibleIDs []string // USB\Class_ff&SubClass_42&Prot_01, USB\COMPOSITE, ...
	LocationPaths []string // PCIROOT(0)#PCI(1400)#USBROOT(0)#USB(2)#USB(1)[#USBMI(1)]
	Address       uint32   // the port number on the parent hub
}

var (
	instanceIDPattern = regexp.MustCompile(`(?i)^USB\\VID_([0-9A-F]{4})&PID_([0-9A-F]{4})(?:&MI_([0-9A-F]{2}))?\\`)
	classPattern      = regexp.MustCompile(`(?i)^USB\\Class_([0-9A-F]{2})&SubClass_([0-9A-F]{2})&Prot_([0-9A-F]{2})$`)
	locationPattern   = regexp.MustCompile(`(?i)^(.+#USBROOT\(\d+\))((?:#USB\(\d+\))*)(?:#USBMI\(\d+\))?$`)
	portPattern       = regexp.MustCompile(`(?i)#USB\((\d+)\)`)
)

// parseInstanceID returns the IDs in a device instance ID and the interface number, -1 for the
// device node itself. ok is false for nodes without IDs, such as root hubs.
func parseInstanceID(id string) (vendor, product uint16, iface int, ok bool) {
	m := instanceIDPattern.FindStringSubmatch(id)
	if m == nil {
		return 0, 0, 0, false
	}
	v, _ := strconv.ParseUint(m[1], 16, 16)
	p, _ := strconv.ParseUint(m[2], 16, 16)
	iface = -1
	if m[3] != "" {
		n, _ := strconv.ParseUint(m[3], 16, 8)
		iface = int(n)
	}
	return uint16(v), uint16(p), iface, true
}

// instanceSerial returns the serial number in a device node's instance ID, or "" when Windows
// generated that part (it then contains '&', as for interface nodes and devices without a serial).
func instanceSerial(id string) string {
	parts := strings.Split(id, `\`)
	if len(parts) != 3 || parts[2] == "" || strings.Contains(parts[2], "&") {
		return ""
	}
	return parts[2]
}

// parseClass returns the class triple among a node's compatible IDs. Windows builds them from the
// interface descriptor for an interface node, and for a device with a single interface.
func parseClass(ids []string) (Interface, bool) {
	for _, id := range ids {
		if m := classPattern.FindStringSubmatch(id); m != nil {
			c, _ := strconv.ParseUint(m[1], 16, 8)
			s, _ := strconv.ParseUint(m[2], 16, 8)
			p, _ := strconv.ParseUint(m[3], 16, 8)
			return Interface{Class: uint8(c), SubClass: uint8(s), Protocol: uint8(p)}, true
		}
	}
	return Interface{}, false
}

// parseLocation turns a location path into a stable bus identifier and its port chain. USBROOT's
// index is local to a host controller, so the bus includes the entire prefix through USBROOT.
// Hashing that prefix, rather than assigning indices during enumeration, keeps a device's location
// stable across mode changes and when other controllers or devices appear. The positive 31-bit
// value fits int on every supported architecture. ok is false when no path has the USB form.
func parseLocation(paths []string) (bus int, ports []int, ok bool) {
	for _, path := range paths {
		m := locationPattern.FindStringSubmatch(path)
		if m == nil {
			continue
		}
		h := fnv.New32a()
		_, _ = h.Write([]byte(strings.ToUpper(m[1])))
		bus = int(h.Sum32() & 0x7fffffff)
		for _, p := range portPattern.FindAllStringSubmatch(m[2], -1) {
			n, _ := strconv.Atoi(p[1])
			ports = append(ports, n)
		}
		return bus, ports, true
	}
	return 0, nil, false
}

// devicesFromNodes groups interface nodes under their device: an interface's location path is its
// device's path followed by #USBMI(n), so both reduce to the same bus and ports.
func devicesFromNodes(nodes []winNode) []DeviceInfo {
	type key struct {
		vendor, product uint16
		location        string
	}
	var order []key
	devices := map[key]*DeviceInfo{}
	for _, n := range nodes {
		vendor, product, iface, ok := parseInstanceID(n.InstanceID)
		if !ok {
			continue
		}
		bus, ports, _ := parseLocation(n.LocationPaths)
		info := DeviceInfo{VendorID: vendor, ProductID: product, Bus: bus, Path: ports}
		k := key{vendor, product, info.Location()}
		d, seen := devices[k]
		if !seen {
			d = &info
			devices[k] = d
			order = append(order, k)
		}
		if iface < 0 {
			d.Address = int(n.Address)
			d.Serial = instanceSerial(n.InstanceID)
		}
		if c, ok := parseClass(n.CompatibleIDs); ok && !slices.Contains(d.Interfaces, c) {
			d.Interfaces = append(d.Interfaces, c)
		}
	}
	out := make([]DeviceInfo, 0, len(order))
	for _, k := range order {
		out = append(out, *devices[k])
	}
	slices.SortStableFunc(out, func(a, b DeviceInfo) int { return strings.Compare(a.Location(), b.Location()) })
	return out
}
