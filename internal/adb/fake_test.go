// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package adb

import (
	"encoding/binary"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
)

// fakeDevice emulates the shell and sync services of an adbd, for both the fake server and the
// fake USB adbd.
type fakeDevice struct {
	mu       sync.Mutex
	files    map[string]*fakeFile
	dirs     map[string]bool
	commands map[string]fakeCmd // by the command the test runs
	failSend map[string]string  // path -> FAIL message for SEND
	// ignoreMode creates every pushed file 0666, like the UFI003's adbd.
	ignoreMode bool
	// pty translates \n to \r\n in legacy shell output, as a terminal where stty did not work.
	pty    bool
	chunk  int      // DATA size for RECV; 0 means syncChunk
	shells []string // services run, in order
}

type fakeFile struct {
	data     []byte
	perm     uint32
	sizeZero bool // STAT reports size 0, like files in /proc
}

type fakeCmd struct {
	stdout, stderr string
	code           int
	block          bool // runs until the host closes the service
}

func newFakeDevice() *fakeDevice {
	return &fakeDevice{
		files:    map[string]*fakeFile{},
		dirs:     map[string]bool{"/etc": true},
		commands: map[string]fakeCmd{},
		failSend: map[string]string{},
	}
}

// file returns a copy of a file, nil when missing.
func (d *fakeDevice) file(path string) *fakeFile {
	d.mu.Lock()
	defer d.mu.Unlock()
	if f := d.files[path]; f != nil {
		c := *f
		return &c
	}
	return nil
}

// set changes the device's settings while it may be serving.
func (d *fakeDevice) set(f func()) {
	d.mu.Lock()
	defer d.mu.Unlock()
	f()
}

func (d *fakeDevice) serve(rw io.ReadWriteCloser, name string) {
	defer rw.Close()
	d.mu.Lock()
	d.shells = append(d.shells, name)
	d.mu.Unlock()
	switch {
	case strings.HasPrefix(name, "shell,v2,raw:"):
		d.shellV2(rw, strings.TrimPrefix(name, "shell,v2,raw:"))
	case strings.HasPrefix(name, "shell:"):
		d.shellLegacy(rw, strings.TrimPrefix(name, "shell:"))
	case name == "sync:":
		d.sync(rw)
	}
}

// drain reads the host's input until it closes the service; the channel closes then.
func drain(r io.Reader) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		io.Copy(io.Discard, r)
		close(done)
	}()
	return done
}

// run looks a command up; "chmod <octal> '<path>'" changes a file's mode.
func (d *fakeDevice) run(cmd string) fakeCmd {
	d.mu.Lock()
	defer d.mu.Unlock()
	if f := strings.Fields(cmd); len(f) == 3 && f[0] == "chmod" {
		perm, _ := strconv.ParseUint(f[1], 8, 32)
		path := strings.ReplaceAll(strings.TrimSuffix(strings.TrimPrefix(f[2], "'"), "'"), `'\''`, "'")
		if file := d.files[path]; file != nil {
			file.perm = uint32(perm)
			return fakeCmd{}
		}
		return fakeCmd{stderr: "chmod: no such file\n", code: 1}
	}
	c, ok := d.commands[cmd]
	if !ok {
		return fakeCmd{stderr: "sh: " + cmd + ": not found\n", code: 127}
	}
	return c
}

func (d *fakeDevice) shellV2(rw io.ReadWriter, cmd string) {
	// The first packet must close stdin.
	var hdr [5]byte
	if _, err := io.ReadFull(rw, hdr[:]); err != nil || hdr != [5]byte{shellCloseStdin} {
		return
	}
	closed := drain(rw)
	c := d.run(cmd)
	if c.block {
		<-closed
		return
	}
	if c.stdout != "" {
		writeShellPacket(rw, shellStdout, []byte(c.stdout))
	}
	if c.stderr != "" {
		writeShellPacket(rw, shellStderr, []byte(c.stderr))
	}
	writeShellPacket(rw, shellExit, []byte{byte(c.code)})
}

func (d *fakeDevice) shellLegacy(rw io.ReadWriter, wrapped string) {
	closed := drain(rw)
	d.mu.Lock()
	pty := d.pty
	d.mu.Unlock()
	prefix, suffix, _ := strings.Cut(sentinelCommand("\x00"), "\x00")
	cmd, okPrefix := strings.CutPrefix(wrapped, prefix)
	cmd, okSuffix := strings.CutSuffix(cmd, suffix)
	if !okPrefix || !okSuffix {
		io.WriteString(rw, "fake: not a sentinel command\n")
		return
	}
	c := d.run(cmd)
	if c.block {
		<-closed
		return
	}
	out := c.stdout + c.stderr + "\n" + sentinel + strconv.Itoa(c.code) + "\n"
	if pty {
		out = strings.ReplaceAll(out, "\n", "\r\n")
	}
	io.WriteString(rw, out)
}

func (d *fakeDevice) sync(rw io.ReadWriter) {
	le := binary.LittleEndian
	reply := func(id string, payload []byte) {
		syncRequest(rw, id, payload)
	}
	fail := func(msg string) { reply("FAIL", []byte(msg)) }
	for {
		var hdr [8]byte
		if _, err := io.ReadFull(rw, hdr[:]); err != nil {
			return
		}
		id, n := string(hdr[:4]), le.Uint32(hdr[4:])
		arg := make([]byte, n)
		if _, err := io.ReadFull(rw, arg); err != nil {
			return
		}
		path := string(arg)
		d.mu.Lock()
		f, isDir, chunk := d.files[path], d.dirs[path], d.chunk
		d.mu.Unlock()
		switch id {
		case "QUIT":
			return
		case "STAT":
			var st [16]byte
			copy(st[:], "STAT")
			switch {
			case f != nil:
				le.PutUint32(st[4:], modeTypeReg|f.perm)
				if !f.sizeZero {
					le.PutUint32(st[8:], uint32(len(f.data)))
				}
				le.PutUint32(st[12:], 1700000000)
			case isDir:
				le.PutUint32(st[4:], modeTypeDir|0o755)
			}
			rw.Write(st[:])
		case "RECV":
			if f == nil {
				fail("No such file or directory")
				continue
			}
			if chunk == 0 {
				chunk = syncChunk
			}
			for off := 0; off < len(f.data); off += chunk {
				reply("DATA", f.data[off:min(off+chunk, len(f.data))])
			}
			reply("DONE", nil)
		case "SEND":
			p, modeStr, _ := strings.Cut(path, ",")
			mode, err := strconv.ParseUint(modeStr, 10, 32)
			d.mu.Lock()
			msg := d.failSend[p]
			d.mu.Unlock()
			if err != nil {
				msg = "bad mode " + modeStr
			}
			if msg != "" {
				// Like old adbd: answer at once, then read the data to its end.
				fail(msg)
			}
			var data []byte
			for {
				if _, err := io.ReadFull(rw, hdr[:]); err != nil {
					return
				}
				n := le.Uint32(hdr[4:])
				if string(hdr[:4]) == "DONE" {
					break
				}
				chunk := make([]byte, n)
				if _, err := io.ReadFull(rw, chunk); err != nil {
					return
				}
				data = append(data, chunk...)
			}
			if msg != "" {
				continue
			}
			d.mu.Lock()
			perm := uint32(mode) & 0o777
			if d.ignoreMode {
				perm = 0o666
			}
			d.files[p] = &fakeFile{data: data, perm: perm}
			d.mu.Unlock()
			reply("OKAY", nil)
		default:
			fail(fmt.Sprintf("unknown sync request %q", id))
			return
		}
	}
}
