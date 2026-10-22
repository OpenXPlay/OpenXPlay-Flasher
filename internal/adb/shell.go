// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package adb

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strconv"
)

// Shell protocol v2 ("shell,v2,raw:"): both directions carry packets of a 1-byte id, a 4-byte
// little-endian length and the data.
const (
	shellStdout     = 1
	shellStderr     = 2
	shellExit       = 3
	shellCloseStdin = 4
)

// maxShellOutput bounds what Shell keeps in memory.
const maxShellOutput = 64 << 20

func writeShellPacket(w io.Writer, id byte, data []byte) error {
	p := make([]byte, 5+len(data))
	p[0] = id
	binary.LittleEndian.PutUint32(p[1:], uint32(len(data)))
	copy(p[5:], data)
	_, err := w.Write(p)
	return err
}

// runShellV2 closes the command's stdin, then collects stdout and stderr until the exit packet.
func runShellV2(rw io.ReadWriter) (Result, error) {
	if err := writeShellPacket(rw, shellCloseStdin, nil); err != nil {
		return Result{}, err
	}
	var r Result
	var hdr [5]byte
	for {
		if _, err := io.ReadFull(rw, hdr[:]); err != nil {
			if errors.Is(err, io.EOF) {
				err = errors.New("adb: shell ended without an exit status")
			}
			return Result{}, err
		}
		n := binary.LittleEndian.Uint32(hdr[1:])
		if int64(len(r.Stdout))+int64(len(r.Stderr))+int64(n) > maxShellOutput {
			return Result{}, fmt.Errorf("adb: shell output exceeds %d bytes", maxShellOutput)
		}
		data := make([]byte, n)
		if _, err := io.ReadFull(rw, data); err != nil {
			return Result{}, err
		}
		switch hdr[0] {
		case shellStdout:
			r.Stdout = append(r.Stdout, data...)
		case shellStderr:
			r.Stderr = append(r.Stderr, data...)
		case shellExit:
			if len(data) != 1 {
				return Result{}, fmt.Errorf("adb: shell exit packet of %d bytes", len(data))
			}
			r.ExitCode = int(data[0])
			return r, nil
		}
	}
}

// The legacy shell service ("shell:") reports no exit status and, on old adbd, runs the command on
// a terminal that turns \n into \r\n and merges stderr into stdout. sentinelCommand turns that
// translation off where stty exists (stock Android 4.4 has none), runs cmd in a subshell with
// stdin from /dev/null (so exit and reads of stdin end it), saves its status and prints it on a
// line of its own with echo only (Android 4.4's shell has no printf). parseSentinel removes that
// line whether or not the terminal translates newlines.
const sentinel = "__OXPF_EXIT__"

func sentinelCommand(cmd string) string {
	return "stty -onlcr 2>/dev/null; (" + cmd + "\n) </dev/null; rc=$?; echo; echo " + sentinel + "$rc"
}

func parseSentinel(out []byte) (Result, error) {
	i := bytes.LastIndex(out, []byte("\n"+sentinel))
	if i < 0 {
		return Result{}, errors.New("adb: shell ended without an exit status")
	}
	status, _, _ := bytes.Cut(out[i+1+len(sentinel):], []byte("\n"))
	status = bytes.TrimSuffix(status, []byte("\r"))
	code, err := strconv.Atoi(string(status))
	if err != nil {
		return Result{}, fmt.Errorf("adb: bad shell exit status %q", status)
	}
	// Our newline came out as \r\n if the terminal still translated.
	out = bytes.TrimSuffix(out[:i], []byte("\r"))
	return Result{Stdout: out, ExitCode: code}, nil
}
