// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package testssh

import (
	"bufio"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
)

// N1 scripts the board facts and a small configuration file, never executing host commands.
type N1 struct {
	Server     *Server
	mu         sync.Mutex
	model, mac string
	Password   string
	Commands   []string
}

func NewN1(t testing.TB, password string) *N1 {
	n := &N1{model: "Phicomm N1", mac: "02:11:22:33:44:55", Password: password}
	n.Server = New(t, password, nil, n.handle)
	return n
}
func (n *N1) SetModel(model string) { n.mu.Lock(); n.model = model; n.mu.Unlock() }
func (n *N1) SetMAC(mac string)     { n.mu.Lock(); n.mac = mac; n.mu.Unlock() }
func (n *N1) handle(cmd string, in io.Reader, out, stderr io.Writer) int {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.Commands = append(n.Commands, cmd)
	if strings.Contains(cmd, "sudo -n -p '' -- true") {
		return Result(out, stderr, "", "sudo: a password is required", 1)
	}
	if strings.Contains(cmd, "sudo -k -S") {
		reader := bufio.NewReader(in)
		password, _ := reader.ReadString('\n')
		if password != n.Password+"\n" {
			return Result(out, stderr, "", "sudo: 1 incorrect password attempt", 1)
		}
		in = reader
	}
	if strings.HasSuffix(cmd, "sudo -k -S -p '' -- true") {
		return 0
	}
	command := Unwrap(cmd)
	switch {
	case command == "cat /sys/class/net/eth0/address":
		fmt.Fprintln(out, n.mac)
	case strings.Contains(command, "printf 'root\\t'"):
		fmt.Fprintf(out, "root\t0\nkernel\t6.12.112-ophub\nmodel\t%s\ndebian\t13\narch\tarm64\npretty\tDebian GNU/Linux 13 (trixie)\n", n.model)
	case strings.HasPrefix(command, "dpkg-query "):
		fmt.Fprint(out, "installed\t1.0")
	case strings.Contains(command, "df -Pk"):
		fmt.Fprint(out, "Filesystem 1024-blocks Used Available Capacity Mounted\n/dev/root 1000000 200000 800000 20% /\n")
	case strings.Contains(command, "identity.pk8"):
		fmt.Fprint(out, "0\n0\n")
	case strings.Contains(command, "readlink -f"):
		fmt.Fprint(out, "file\n/var/lib/openxplay/settings.json\n600\nroot:root\n2\n")
	case strings.Contains(command, "exec 3<"):
		fmt.Fprint(out, "{}")
	default:
		return Result(out, stderr, "", "unscripted fixture command", 127)
	}
	return 0
}
