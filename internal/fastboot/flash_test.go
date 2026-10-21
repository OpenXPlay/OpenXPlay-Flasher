// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package fastboot

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"testing"
	"testing/synctest"
	"time"
)

func flashOK() bulkStep {
	s := in("OKAY")
	s.deadline = flashTimeout
	return s
}

func flashSteps(partition string, data []byte) []bulkStep {
	size := len(data)
	steps := []bulkStep{out(fmt.Sprintf("download:%08x", size)), in(fmt.Sprintf("DATA%08x", size))}
	for offset := 0; offset < size; offset += transferSize {
		steps = append(steps, payload(data[offset:min(size, offset+transferSize)]))
	}
	steps = append(steps, in("OKAY"), out("flash:"+partition), flashOK())
	return steps
}

func TestFlashStockRawAndCachedLimit(t *testing.T) {
	data := bytes.Repeat([]byte{0x5a}, 512)
	img := openFixture(t, data)
	steps := []bulkStep{
		out("oem device-info"), in("INFODevice unlocked: false"), in("OKAY"),
		out("getvar:max-download-size"), in("OKAY0x10000000"),
		out("getvar:partition-size:aboot"), in("OKAY"), // Stock LK's unknown getvar is empty.
	}
	steps = append(steps, flashSteps("aboot", data)...)
	steps = append(steps, out("getvar:partition-size:aboot"), in("FAILunknown variable"))
	steps = append(steps, flashSteps("aboot", data)...)
	c, _ := script(t, steps...)
	if _, err := c.OEM(t.Context(), "device-info"); err != nil {
		t.Fatal(err)
	}
	// Stock LK accepts aboot despite unlocked: false; no invented unlock policy gates Flash.
	for range 2 {
		if err := c.Flash(context.Background(), "aboot", img, FlashOptions{}, nil); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFlashPiecesAndProgress(t *testing.T) {
	for _, sparse := range []bool{false, true} {
		t.Run(fmt.Sprint(sparse), func(t *testing.T) {
			data := bytes.Repeat([]byte{0x1b}, 9001)
			limit := int64(4096 + 64)
			if sparse {
				data = fixtureSparse(4, []testChunk{
					{chunkDontCare, 3, nil}, {chunkRaw, 8, bytes.Repeat([]byte{3}, 32)},
					{chunkDontCare, 3, nil}, {chunkFill, 5, []byte{1, 2, 3, 4}},
					{chunkRaw, 13, bytes.Repeat([]byte{7}, 52)}, {chunkCRC32, 0, make([]byte, 4)},
				}, false)
				limit = 92
			}
			img := openFixture(t, data)
			pieces, err := img.pieces(t.Context(), limit)
			if err != nil || len(pieces) < 2 {
				t.Fatalf("pieces: %d, %v", len(pieces), err)
			}
			steps := []bulkStep{out("getvar:max-download-size"), in("OKAY268435456"), out("getvar:partition-size:rootfs"), in(fmt.Sprintf("OKAY%d", img.Size()))}
			for _, piece := range pieces {
				wire, err := io.ReadAll(piece.reader(t.Context()))
				if err != nil {
					t.Fatal(err)
				}
				steps = append(steps, flashSteps("rootfs", wire)...)
			}
			c, _ := script(t, steps...)
			var events []Event
			if err := c.Flash(context.Background(), "rootfs", img, FlashOptions{MaxPiece: limit}, func(e Event) { events = append(events, e) }); err != nil {
				t.Fatal(err)
			}
			var phases []string
			var previous Event
			for _, e := range events {
				if e.Pieces != len(pieces) || e.Piece < 1 || e.Piece > len(pieces) || e.Total != pieces[e.Piece-1].size || e.Done < 0 || e.Done > e.Total {
					t.Fatalf("progress bounds: %+v", e)
				}
				if e.Piece != previous.Piece || e.Phase != previous.Phase {
					if previous.Phase != "" && previous.Done != previous.Total {
						t.Fatalf("incomplete phase: %+v", previous)
					}
					if e.Done != 0 {
						t.Fatalf("phase starts above zero: %+v", e)
					}
					phases = append(phases, fmt.Sprintf("%s %d/%d", e.Phase, e.Piece, e.Pieces))
				} else if e.Done < previous.Done {
					t.Fatal("progress went backwards")
				}
				previous = e
			}
			var want []string
			for i := range pieces {
				want = append(want, fmt.Sprintf("sending %d/%d", i+1, len(pieces)), fmt.Sprintf("writing %d/%d", i+1, len(pieces)))
			}
			if !reflect.DeepEqual(phases, want) || previous.Done != previous.Total {
				t.Fatalf("phases: %v, want %v", phases, want)
			}
		})
	}
}

func TestFlashLimits(t *testing.T) {
	for _, tc := range []struct {
		name                      string
		device, option, firstSize int64
	}{
		// A RAW piece's 28 + 12 + 12 bytes of overhead leave 51199 complete 4096-byte blocks.
		{"lk1st 200 MiB cap", 256 << 20, 0, 51199*4096 + 52},
		{"smaller device", 1 << 20, 0, 255*4096 + 52},
		{"override", 256 << 20, 2 << 20, 511*4096 + 52},
		{"override capped at device", 1 << 20, 2 << 20, 255*4096 + 52},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := &generatedSource{size: 201<<20 + 1}
			img := &Image{source: source, stored: source.size}
			c, _ := script(t,
				out("getvar:max-download-size"), in(fmt.Sprintf("OKAY%d", tc.device)),
				out("getvar:partition-size:rootfs"), in("OKAY0xd4dfbe00"),
				out(fmt.Sprintf("download:%08x", tc.firstSize)), in("FAILstop before data"),
			)
			var fail *FailError
			if err := c.Flash(t.Context(), "rootfs", img, FlashOptions{MaxPiece: tc.option}, nil); !errors.As(err, &fail) {
				t.Fatalf("first piece: %v", err)
			}
			if source.calls != 0 {
				t.Fatal("planning a flash fetched payload")
			}
		})
	}
}

func TestFlashPreflight(t *testing.T) {
	for _, sparse := range []bool{false, true} {
		data := []byte("raw image")
		if sparse {
			data = fixtureSparse(4096, []testChunk{{chunkDontCare, 100, nil}, {chunkFill, 200, []byte{1, 2, 3, 4}}}, false)
		}
		img := openFixture(t, data)
		c, _ := script(t, out("getvar:max-download-size"), in("OKAY0x10000000"), out("getvar:partition-size:boot"), in(fmt.Sprintf("OKAY%d", img.Size()-1)))
		var size *PartitionSizeError
		if err := c.Flash(t.Context(), "boot", img, FlashOptions{}, nil); !errors.As(err, &size) || size.ImageSize != img.Size() || size.PartitionSize != img.Size()-1 {
			t.Fatalf("capacity: %v", err)
		}
	}
	img := rawFixture([]byte("raw image"))
	for _, value := range []string{"FAILnot found", "OKAY"} {
		steps := []bulkStep{out("getvar:max-download-size"), in("OKAY0x10000000"), out("getvar:partition-size:boot"), in(value)}
		steps = append(steps, flashSteps("boot", []byte("raw image"))...)
		c, _ := script(t, steps...)
		if err := c.Flash(context.Background(), "boot", img, FlashOptions{}, nil); err != nil {
			t.Fatal(err)
		}
	}
	c, _ := script(t, out("getvar:max-download-size"), in("OKAY0x10000000"), out("getvar:partition-size:boot"), in("OKAYnot a size"))
	var value *ValueError
	if err := c.Flash(t.Context(), "boot", img, FlashOptions{}, nil); !errors.As(err, &value) {
		t.Fatalf("invalid capacity: %v", err)
	}
	c, _ = script(t, out("getvar:max-download-size"), in("FAILunsupported"))
	for range 2 {
		var fail *FailError
		if err := c.Flash(t.Context(), "boot", img, FlashOptions{}, nil); !errors.As(err, &fail) {
			t.Fatalf("unknown max-download-size: %v", err)
		}
	}
	c, _ = script(t)
	for _, tc := range []struct {
		partition string
		img       *Image
		opts      FlashOptions
	}{
		{"", img, FlashOptions{}}, {"boot", nil, FlashOptions{}}, {"boot", img, FlashOptions{MaxPiece: -1}},
	} {
		var command *CommandError
		if err := c.Flash(t.Context(), tc.partition, tc.img, tc.opts, nil); !errors.As(err, &command) {
			t.Fatalf("invalid flash: %v", err)
		}
	}
	// A sparse input is sent as sparse even if its small encoded length fits the limit.
	img = sparseFixture(t, fixtureSparse(4, []testChunk{{chunkFill, 1, make([]byte, 4)}}, false))
	c, _ = script(t, out("getvar:max-download-size"), in("OKAY1000"))
	var limit *PieceLimitError
	if err := c.Flash(t.Context(), "boot", img, FlashOptions{MaxPiece: 40}, nil); !errors.As(err, &limit) {
		t.Fatalf("unusable sparse limit: %v", err)
	}
}

func TestFlashFailureStopsPieces(t *testing.T) {
	img := rawFixture(bytes.Repeat([]byte{3}, 9000))
	pieces, err := img.pieces(t.Context(), 4160)
	if err != nil {
		t.Fatal(err)
	}
	steps := []bulkStep{out("getvar:max-download-size"), in("OKAY100000"), out("getvar:partition-size:boot"), in("OKAY100000")}
	for i, piece := range pieces[:2] {
		data, err := io.ReadAll(piece.reader(t.Context()))
		if err != nil {
			t.Fatal(err)
		}
		s := flashSteps("boot", data)
		if i == 1 {
			s[len(s)-1] = in("FAILwrite protected")
			s[len(s)-1].deadline = flashTimeout
		}
		steps = append(steps, s...)
	}
	steps = append(steps, out("reboot"), in("OKAY"))
	c, _ := script(t, steps...)
	var fail *FailError
	if err := c.Flash(context.Background(), "boot", img, FlashOptions{MaxPiece: 4160}, nil); !errors.As(err, &fail) || fail.Command != "flash:boot" {
		t.Fatalf("flash failure: %v", err)
	}
	if err := c.Reboot(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestFlashTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		data := []byte("boot")
		steps := []bulkStep{out("getvar:max-download-size"), in("OKAY0x10000000"), out("getvar:partition-size:boot"), in("OKAY0x4000000")}
		steps = append(steps, flashSteps("boot", data)...)
		steps[len(steps)-1] = bulkStep{stall: true, buffer: 256, deadline: flashTimeout}
		c, _ := script(t, steps...)
		start := time.Now()
		err := c.Flash(context.Background(), "boot", rawFixture(data), FlashOptions{}, nil)
		var op *OpError
		if !errors.As(err, &op) || !errors.Is(err, context.DeadlineExceeded) || time.Since(start) != 15*time.Minute {
			t.Fatalf("flash timeout after %v: %v", time.Since(start), err)
		}
		if err := c.Reboot(t.Context()); !errors.Is(err, ErrBroken) {
			t.Fatalf("after flash timeout: %v", err)
		}
	})
}

func TestLK1stSlowWrite(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// A 200 MB piece took 73 s on the real UFI003. Status must outlive the ordinary 10 s
		// command budget; model the wait without sleeping or using hardware.
		data := []byte("boot")
		gate := make(chan struct{})
		steps := []bulkStep{out("getvar:max-download-size"), in("OKAY0x10000000"), out("getvar:partition-size:boot"), in("OKAY0x4000000")}
		steps = append(steps, flashSteps("boot", data)...)
		steps[len(steps)-1].gate = gate
		steps[len(steps)-1].data = []byte("OKAYwritten")
		c, _ := script(t, steps...)
		go func() { time.Sleep(73 * time.Second); close(gate) }()
		start := time.Now()
		if err := c.Flash(context.Background(), "boot", rawFixture(data), FlashOptions{}, nil); err != nil {
			t.Fatal(err)
		}
		if time.Since(start) != 73*time.Second {
			t.Fatalf("write took %v", time.Since(start))
		}
	})
}

func TestEntireFlashIsSerialised(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		img := rawFixture(bytes.Repeat([]byte{3}, 9000))
		pieces, err := img.pieces(t.Context(), 4160)
		if err != nil {
			t.Fatal(err)
		}
		steps := []bulkStep{out("getvar:max-download-size"), in("OKAY100000"), out("getvar:partition-size:boot"), in("OKAY100000")}
		for _, piece := range pieces {
			data, err := io.ReadAll(piece.reader(t.Context()))
			if err != nil {
				t.Fatal(err)
			}
			steps = append(steps, flashSteps("boot", data)...)
		}
		steps = append(steps, out("getvar:product"), in("OKAYMSM8916"))
		c, _ := script(t, steps...)
		gate := make(chan struct{})
		done, other := make(chan error, 1), make(chan error, 1)
		go func() {
			done <- c.Flash(context.Background(), "boot", img, FlashOptions{MaxPiece: 4160}, func(e Event) {
				if e.Piece == 1 && e.Phase == "writing" && e.Done == e.Total {
					<-gate
				}
			})
		}()
		synctest.Wait() // Flash is deliberately parked between its first and second pieces.
		go func() { _, err := c.GetVar(context.Background(), "product"); other <- err }()
		synctest.Wait()
		close(gate)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if err := <-other; err != nil {
			t.Fatal(err)
		}
	})
}
