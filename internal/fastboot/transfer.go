// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package fastboot

import (
	"context"
	"fmt"
	"io"
)

// Download stages exactly size bytes for a later command, using OUT transfers of at most 1 MiB.
// It never sends a terminating ZLP: fastboot counts bytes, so a ZLP could become an empty command.
// A reader must arrange its own cancellation if Read can block independently of USB.
func (c *Client) Download(ctx context.Context, r io.Reader, size int64, p Progress) error {
	if err := c.lock(ctx); err != nil {
		return err
	}
	defer c.unlock()
	_, err := c.download(ctx, r, size, p, 1, 1)
	return err
}

func (c *Client) download(ctx context.Context, r io.Reader, size int64, p Progress, piece, pieces int) (Response, error) {
	if size < 0 || size > maxDownload || r == nil {
		return Response{}, &CommandError{Command: "download", Message: "need a reader and a size from 0 to 0xffffffff"}
	}
	cmd := fmt.Sprintf("download:%08x", size)
	s, err := c.start(ctx, cmd, commandTimeout)
	if err != nil {
		return s.Response, err
	}
	if s.data != size {
		return s.Response, c.protocolError(cmd, fmt.Sprintf("DATA size %d does not match %d", s.data, size))
	}
	progress(p, "sending", piece, pieces, 0, size, nil)
	buf := make([]byte, min(int64(transferSize), size))
	var done int64
	for done < size {
		if err := ctx.Err(); err != nil {
			return s.Response, c.interrupted(ctx, cmd, "sending", s.Info, err)
		}
		data := buf[:min(int64(len(buf)), size-done)]
		if _, err := io.ReadFull(contextReader{ctx: ctx, r: r}, data); err != nil {
			return s.Response, c.interrupted(ctx, cmd, "sending", s.Info, err)
		}
		if err := c.write(ctx, cmd, "sending", data, s.Info); err != nil {
			return s.Response, err
		}
		done += int64(len(data))
		progress(p, "sending", piece, pieces, done, size, nil)
	}
	rsp, err := c.finish(ctx, cmd, s.Info)
	if len(rsp.Info) > 0 {
		progress(p, "sending", piece, pieces, done, size, rsp.Info)
	}
	return rsp, err
}

func (c *Client) write(ctx context.Context, cmd, phase string, data []byte, info []string) error {
	if err := ctx.Err(); err != nil {
		return c.interrupted(ctx, cmd, phase, info, err)
	}
	n, err := c.bulk.Write(ctx, data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	if err != nil {
		return c.interrupted(ctx, cmd, phase, info, err)
	}
	return nil
}

// Upload reads the data staged by the device (for example by "oem dump fsc") into w, then waits
// for the final OKAY. Short packets do not end a known-length data phase. The returned count is
// the number of bytes written to w, even on failure; w must arrange its own blocking cancellation.
func (c *Client) Upload(ctx context.Context, w io.Writer, p Progress) (int64, error) {
	if err := c.lock(ctx); err != nil {
		return 0, err
	}
	defer c.unlock()
	if w == nil {
		return 0, &CommandError{Command: "upload", Message: "nil writer"}
	}
	s, err := c.start(ctx, "upload", commandTimeout)
	if err != nil {
		return 0, err
	}
	if s.data < 0 {
		return 0, c.protocolError("upload", "expected DATA before the uploaded bytes")
	}
	progress(p, "receiving", 1, 1, 0, s.data, nil)
	buf := make([]byte, min(int64(transferSize), s.data))
	var done int64
	empty := 0
	for done < s.data {
		if err := ctx.Err(); err != nil {
			return done, c.interrupted(ctx, "upload", "receiving", s.Info, err)
		}
		data := buf[:min(int64(len(buf)), s.data-done)]
		n, readErr := c.bulk.Read(ctx, data)
		if n < 0 || n > len(data) {
			return done, c.protocolError("upload", fmt.Sprintf("data transfer returned %d bytes for a %d-byte buffer", n, len(data)))
		}
		if n > 0 {
			empty = 0
			nw, err := w.Write(data[:n])
			if nw < 0 || nw > n {
				return done, c.interrupted(ctx, "upload", "receiving", s.Info, io.ErrShortWrite)
			}
			done += int64(nw)
			if err == nil && nw != n {
				err = io.ErrShortWrite
			}
			if err != nil {
				return done, c.interrupted(ctx, "upload", "receiving", s.Info, err)
			}
			progress(p, "receiving", 1, 1, done, s.data, nil)
		} else {
			empty++
		}
		if readErr != nil {
			return done, c.interrupted(ctx, "upload", "receiving", s.Info, readErr)
		}
		if empty >= 100 {
			return done, c.interrupted(ctx, "upload", "receiving", s.Info, io.ErrNoProgress)
		}
	}
	rsp, err := c.finish(ctx, "upload", s.Info)
	if len(rsp.Info) > 0 {
		progress(p, "receiving", 1, 1, done, s.data, rsp.Info)
	}
	return done, err
}
