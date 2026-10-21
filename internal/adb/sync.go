// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package adb

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strconv"
	"time"
)

// The sync service ("sync:") takes requests of a 4-byte id, a 4-byte little-endian length and a
// payload. STAT answers mode, size and mtime; RECV answers DATA chunks then DONE; SEND takes
// "path,mode", DATA chunks and DONE with the mtime, and answers OKAY. Errors come as FAIL with a
// message.
const (
	syncChunk   = 64 << 10 // largest DATA payload
	syncMaxPath = 1024
	modeTypeDir = 0o040000
	modeTypeReg = 0o100000
	modeTypeMsk = 0o170000
)

func syncRequest(w io.Writer, id string, payload []byte) error {
	p := make([]byte, 8+len(payload))
	copy(p, id)
	binary.LittleEndian.PutUint32(p[4:], uint32(len(payload)))
	copy(p[8:], payload)
	_, err := w.Write(p)
	return err
}

// readSyncHeader reads an id and its length; a FAIL is returned as an error with its message.
func readSyncHeader(r io.Reader) (string, uint32, error) {
	var hdr [8]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return "", 0, err
	}
	id, n := string(hdr[:4]), binary.LittleEndian.Uint32(hdr[4:])
	if id == "FAIL" {
		if n > syncChunk {
			return "", 0, fmt.Errorf("adb: sync failure message of %d bytes", n)
		}
		msg := make([]byte, n)
		if _, err := io.ReadFull(r, msg); err != nil {
			return "", 0, err
		}
		return "", 0, &SyncError{Message: string(msg)}
	}
	return id, n, nil
}

// SyncError is a failure the device reported over the sync service.
type SyncError struct{ Message string }

func (e *SyncError) Error() string { return "adb: device: " + e.Message }

func checkSyncPath(path string) error {
	if path == "" || len(path) > syncMaxPath {
		return fmt.Errorf("adb: bad device path %q", path)
	}
	return nil
}

// syncStat returns a path's mode, size and mtime; mode is 0 when it does not exist.
func syncStat(rw io.ReadWriter, path string) (mode, size, mtime uint32, err error) {
	if err := syncRequest(rw, "STAT", []byte(path)); err != nil {
		return 0, 0, 0, err
	}
	var resp [16]byte
	if _, err := io.ReadFull(rw, resp[:]); err != nil {
		return 0, 0, 0, err
	}
	if string(resp[:4]) != "STAT" {
		return 0, 0, 0, fmt.Errorf("adb: unexpected sync reply %q to STAT", resp[:4])
	}
	le := binary.LittleEndian
	return le.Uint32(resp[4:]), le.Uint32(resp[8:]), le.Uint32(resp[12:]), nil
}

func syncPull(rw io.ReadWriter, path string, max int64) ([]byte, error) {
	if err := checkSyncPath(path); err != nil {
		return nil, err
	}
	mode, size, _, err := syncStat(rw, path)
	switch {
	case err != nil:
		return nil, err
	case mode == 0:
		return nil, &fs.PathError{Op: "pull", Path: path, Err: fs.ErrNotExist}
	case mode&modeTypeMsk == modeTypeDir:
		return nil, &fs.PathError{Op: "pull", Path: path, Err: errors.New("is a directory")}
	case int64(size) > max:
		return nil, fmt.Errorf("%w: %s has %d bytes, the limit is %d", ErrTooLarge, path, size, max)
	}
	// STAT's size is 32 bits and files such as those in /proc report 0, so the limit holds while
	// reading too.
	if err := syncRequest(rw, "RECV", []byte(path)); err != nil {
		return nil, err
	}
	data := make([]byte, 0, size)
	for {
		id, n, err := readSyncHeader(rw)
		if err != nil {
			return nil, err
		}
		switch id {
		case "DONE":
			_ = syncRequest(rw, "QUIT", nil)
			return data, nil
		case "DATA":
			if n > syncChunk {
				return nil, fmt.Errorf("adb: sync DATA of %d bytes", n)
			}
			if int64(len(data))+int64(n) > max {
				return nil, fmt.Errorf("%w: %s has more than %d bytes", ErrTooLarge, path, max)
			}
			data = append(data, make([]byte, n)...)
			if _, err := io.ReadFull(rw, data[len(data)-int(n):]); err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("adb: unexpected sync reply %q to RECV", id)
		}
	}
}

func syncPush(rw io.ReadWriter, path string, data []byte, mode fs.FileMode, mtime time.Time) error {
	if err := checkSyncPath(path); err != nil {
		return err
	}
	// The mode goes in decimal, with the regular file type bits as adb clients send it.
	spec := path + "," + strconv.FormatUint(uint64(modeTypeReg|mode.Perm()), 10)
	err := syncRequest(rw, "SEND", []byte(spec))
	for off := 0; err == nil && off < len(data); off += syncChunk {
		err = syncRequest(rw, "DATA", data[off:min(off+syncChunk, len(data))])
	}
	if err == nil {
		var done [8]byte
		copy(done[:], "DONE")
		binary.LittleEndian.PutUint32(done[4:], uint32(mtime.Unix()))
		_, err = rw.Write(done[:])
	}
	// A device that refused the file may have answered FAIL and closed early: its reason is
	// more useful than the write error.
	id, _, rerr := readSyncHeader(rw)
	var se *SyncError
	switch {
	case errors.As(rerr, &se):
		return rerr
	case err != nil:
		return err
	case rerr != nil:
		return rerr
	case id != "OKAY":
		return fmt.Errorf("adb: unexpected sync reply %q to SEND", id)
	}
	return nil
}
