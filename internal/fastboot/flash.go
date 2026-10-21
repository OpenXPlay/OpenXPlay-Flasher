// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package fastboot

import (
	"context"
	"io"
)

// defaultMaxPiece is a device limit, not an estimate: UFI003's lk1st advertises 256 MiB, but that
// transfer size makes it reboot with usb_write failed. OpenStick uses fastboot -S 200M for this.
const defaultMaxPiece = int64(200 << 20)

// FlashOptions bounds the bytes in each download, including sparse headers. Zero uses the smaller
// of 200 MiB and max-download-size. A positive override is still capped at the device/protocol limit.
type FlashOptions struct{ MaxPiece int64 }

// Flash checks capacity, then downloads and writes each piece in order. Small raw files retain
// their exact bytes; sparse files and large raw files use complete sparse images with holes around
// each piece's blocks. img must remain open and unchanged until Flash returns.
func (c *Client) Flash(ctx context.Context, partition string, img *Image, opts FlashOptions, p Progress) error {
	if err := c.lock(ctx); err != nil {
		return err
	}
	defer c.unlock()
	cmd := "flash:" + partition
	if err := c.ready(ctx); err != nil {
		return err
	}
	if err := validateCommand(cmd); err != nil {
		return err
	}
	if partition == "" || img == nil || opts.MaxPiece < 0 {
		return &CommandError{Command: cmd, Message: "need a partition, an image and a nonnegative MaxPiece"}
	}
	deviceLimit, err := c.maxDownloadSize(ctx)
	if err != nil {
		return err
	}
	limit := opts.MaxPiece
	if limit == 0 {
		limit = defaultMaxPiece
	}
	limit = min(limit, deviceLimit, maxDownload)

	var pieces []sparsePiece
	if img.Sparse() || img.Size() > limit {
		pieces, err = img.pieces(ctx, limit)
		if err != nil {
			return err
		}
	}
	// Very long partition names can fit flash: but not the optional getvar command.
	if validateCommand("getvar:partition-size:"+partition) == nil {
		capacity, err := c.partitionSize(ctx, partition)
		if err != nil && !partitionUnavailable(err) {
			return err
		}
		if err == nil && img.Size() > capacity {
			return &PartitionSizeError{Partition: partition, ImageSize: img.Size(), PartitionSize: capacity}
		}
	}
	count := max(1, len(pieces))
	for i := range count {
		size := img.Size()
		var r io.Reader = io.NewSectionReader(img.source, 0, size)
		if len(pieces) > 0 {
			size = pieces[i].size
			r = pieces[i].reader(ctx)
		}
		if _, err := c.download(ctx, r, size, p, i+1, count); err != nil {
			return err
		}
		progress(p, "writing", i+1, count, 0, size, nil)
		rsp, err := c.command(ctx, cmd, flashTimeout)
		if err != nil {
			return err
		}
		progress(p, "writing", i+1, count, size, size, rsp.Info)
	}
	return nil
}
