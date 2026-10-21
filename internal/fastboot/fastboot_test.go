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
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/usb"
)

// These values are from the stock LK and OpenStick lk1st UFI003 dongles (fsqo.1, 2026-10-21).
func TestDongleVariables(t *testing.T) {
	if Interface != (usb.Interface{Class: 0xff, SubClass: 0x42, Protocol: 0x03}) {
		t.Fatalf("interface: %+v", Interface)
	}
	for _, device := range []struct {
		name string
		vars [][2]string
	}{
		{"stock", [][2]string{{"product", "MSM8916"}, {"version", "0.5"}, {"kernel", "lk"},
			{"all", ""}, {"unlocked", ""}, {"secure", ""}, {"version-bootloader", ""}}},
		{"lk1st", [][2]string{{"product", "LK1ST_MSM8916"}, {"unlocked", "yes"}, {"all", ""}}},
	} {
		t.Run(device.name, func(t *testing.T) {
			var steps []bulkStep
			for _, v := range device.vars {
				steps = append(steps, out("getvar:"+v[0]), in("INFOquerying "+v[0]), in("TEXTbootloader"), in("OKAY"+v[1]))
			}
			steps = append(steps, out("getvar:max-download-size"), in("OKAY0x10000000"))
			c, _ := script(t, steps...)
			for _, v := range device.vars {
				if got, err := c.GetVar(t.Context(), v[0]); err != nil || got != v[1] {
					t.Fatalf("getvar %s = %q, %v", v[0], got, err)
				}
			}
			for range 2 {
				if got, err := c.MaxDownloadSize(t.Context()); err != nil || got != 256<<20 {
					t.Fatalf("max download size = %d, %v", got, err)
				}
			}
		})
	}
}

func TestCommandsAndInfo(t *testing.T) {
	eraseOK := in("OKAYerased")
	eraseOK.deadline = flashTimeout
	c, _ := script(t,
		out("oem device-info"), in("INFODevice unlocked: false"), in("TEXTstock LK"), in("OKAYdone"),
		out("oem unknown"), in("INFOchecking"), in("TEXTunsupported"), in("FAILunknown command"),
		out("erase:modemst1"), eraseOK,
		out("reboot"), in("OKAY"), out("reboot-bootloader"), in("OKAY"),
	)
	r, err := c.OEM(t.Context(), "device-info")
	if err != nil || r.Message != "done" || !reflect.DeepEqual(r.Info, []string{"Device unlocked: false", "stock LK"}) {
		t.Fatalf("OEM: %+v, %v", r, err)
	}
	r, err = c.OEM(t.Context(), "unknown")
	var fail *FailError
	if !errors.As(err, &fail) || fail.Command != "oem unknown" || fail.Message != "unknown command" ||
		!reflect.DeepEqual(fail.Info, []string{"checking", "unsupported"}) || !reflect.DeepEqual(r.Info, fail.Info) {
		t.Fatalf("FAIL: %+v, %v", r, err)
	}
	if err := c.Erase(t.Context(), "modemst1"); err != nil {
		t.Fatal(err)
	}
	if err := c.Reboot(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := c.RebootBootloader(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestSizeVariables(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  int64
		bad   bool
	}{
		{"0x10000000", 256 << 20, false}, {"268435456", 256 << 20, false}, {"010", 10, false},
		{"0Xff", 255, false}, {"", 0, true}, {"0", 0, true}, {"-1", 0, true}, {"+2", 0, true},
		{"0x", 0, true}, {"0xz1", 0, true}, {"18446744073709551615", 0, true}, {"1_024", 0, true},
	} {
		t.Run(fmt.Sprintf("max-%q", tc.value), func(t *testing.T) {
			c, _ := script(t, out("getvar:max-download-size"), in("OKAY"+tc.value))
			for range 2 {
				n, err := c.MaxDownloadSize(t.Context())
				var value *ValueError
				if tc.bad {
					if !errors.As(err, &value) || value.Name != "max-download-size" {
						t.Fatalf("size = %d, %v", n, err)
					}
				} else if n != tc.want || err != nil {
					t.Fatalf("size = %d, %v", n, err)
				}
			}
		})
	}
	for _, tc := range []struct {
		name, value string
		want        int64
	}{
		{"rootfs", "0xd4dfbe00", 0xd4dfbe00}, {"boot", "0x4000000", 64 << 20},
		{"modemst1", "0x200000", 2 << 20}, {"fsc", "1024", 1024},
	} {
		c, _ := script(t, out("getvar:partition-size:"+tc.name), in("OKAY"+tc.value))
		if n, err := c.PartitionSize(t.Context(), tc.name); err != nil || n != tc.want {
			t.Fatalf("partition %s = %d, %v", tc.name, n, err)
		}
	}
}

func TestCommandValidation(t *testing.T) {
	name := strings.Repeat("v", 57)
	c, _ := script(t, out("getvar:"+name), in("OKAY64 bytes"), out("reboot"), in("OKAY"))
	for _, name := range []string{"", strings.Repeat("x", 58), "non-ASCII-界", "bad\x00", "bad\n"} {
		_, err := c.GetVar(t.Context(), name)
		var command *CommandError
		if !errors.As(err, &command) {
			t.Errorf("getvar %q: %v", name, err)
		}
	}
	if got, err := c.GetVar(t.Context(), name); err != nil || got != "64 bytes" {
		t.Fatalf("64-byte command: %q, %v", got, err)
	}
	for _, run := range []func() error{
		func() error { return c.Erase(t.Context(), "") },
		func() error { return c.Download(t.Context(), strings.NewReader(""), -1, nil) },
		func() error { return c.Download(t.Context(), strings.NewReader(""), 1<<32, nil) },
		func() error { return c.Download(t.Context(), nil, 0, nil) },
		func() error { _, err := c.Upload(t.Context(), nil, nil); return err },
		func() error { _, err := c.OEM(t.Context(), strings.Repeat("x", 61)); return err },
		func() error { _, err := c.PartitionSize(t.Context(), ""); return err },
	} {
		var command *CommandError
		if err := run(); !errors.As(err, &command) {
			t.Errorf("invalid argument: %v", err)
		}
	}
	if err := c.Reboot(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestDownloadExactTransfers(t *testing.T) {
	for _, size := range []int{0, 512, transferSize, transferSize + 512} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			data := bytes.Repeat([]byte{0x5a}, size)
			cmd := fmt.Sprintf("download:%08x", size)
			steps := []bulkStep{out(cmd), in("INFOready"), in(fmt.Sprintf("DATA%08x", size))}
			for offset := 0; offset < len(data); offset += transferSize {
				steps = append(steps, payload(data[offset:min(offset+transferSize, len(data))]))
			}
			// The very next OUT must be a command, even for exact multiples of the 512-byte packet.
			steps = append(steps, in("TEXTreceived"), in("OKAY"), out("getvar:product"), in("OKAYMSM8916"))
			c, _ := script(t, steps...)
			r := bytes.NewReader(append(append([]byte{}, data...), 0xff))
			var events []Event
			if err := c.Download(context.Background(), r, int64(size), func(e Event) { events = append(events, e) }); err != nil {
				t.Fatal(err)
			}
			if r.Len() != 1 {
				t.Fatalf("read beyond declared size: %d bytes left", r.Len())
			}
			last := events[len(events)-1]
			if last.Phase != "sending" || last.Piece != 1 || last.Pieces != 1 || last.Done != int64(size) || last.Total != int64(size) ||
				!reflect.DeepEqual(last.Info, []string{"ready", "received"}) {
				t.Fatalf("progress: %+v", events)
			}
			if got, err := c.GetVar(t.Context(), "product"); err != nil || got != "MSM8916" {
				t.Fatalf("next command: %q, %v", got, err)
			}
		})
	}
}

func TestDataReplies(t *testing.T) {
	for _, reply := range []string{"DAT", "WHAT", "DATA0000000", "DATA000000000", "DATAzz000004", "DATA+0000004", "DATA00000003", "OKAY"} {
		t.Run(reply, func(t *testing.T) {
			c, _ := script(t, out("download:00000004"), in(reply))
			var protocol *ProtocolError
			if err := c.Download(t.Context(), strings.NewReader("abcd"), 4, nil); !errors.As(err, &protocol) {
				t.Fatalf("DATA check: %v", err)
			}
			if _, err := c.GetVar(t.Context(), "product"); !errors.Is(err, ErrBroken) {
				t.Fatalf("after malformed reply: %v", err)
			}
		})
	}
	for _, command := range []string{"getvar:product", "oem dump fsc"} {
		c, _ := script(t, out(command), in("DATA00000000"))
		_, err := c.command(context.Background(), command, commandTimeout)
		var protocol *ProtocolError
		if !errors.As(err, &protocol) || !c.broken {
			t.Fatalf("unexpected DATA on %s: %v", command, err)
		}
	}
	// Mixed-case hexadecimal DATA sizes are valid; a final DATA is not.
	data := bytes.Repeat([]byte{1}, 0xab)
	c, _ := script(t, out("download:000000ab"), in("DATA000000aB"), payload(data), in("DATA000000ab"))
	var protocol *ProtocolError
	if err := c.Download(context.Background(), bytes.NewReader(data), int64(len(data)), nil); !errors.As(err, &protocol) {
		t.Fatalf("final DATA: %v", err)
	}
}

func TestDownloadFailure(t *testing.T) {
	for _, final := range []bool{false, true} {
		steps := []bulkStep{out("download:00000004"), in("INFOinitial")}
		if final {
			steps = append(steps, in("DATA00000004"), payload([]byte("abcd")), in("TEXTfinal"))
		}
		steps = append(steps, in("FAILrefused"), out("reboot"), in("OKAY"))
		c, _ := script(t, steps...)
		err := c.Download(context.Background(), strings.NewReader("abcd"), 4, nil)
		var fail *FailError
		want := []string{"initial"}
		if final {
			want = append(want, "final")
		}
		if !errors.As(err, &fail) || fail.Command != "download:00000004" || !reflect.DeepEqual(fail.Info, want) {
			t.Fatalf("download FAIL: %v", err)
		}
		if err := c.Reboot(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	c, _ := script(t, out("download:00000004"), in("DATA00000004"))
	if err := c.Download(t.Context(), strings.NewReader("ab"), 4, nil); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("truncated reader: %v", err)
	}
	if err := c.Reboot(t.Context()); !errors.Is(err, ErrBroken) {
		t.Fatalf("after short input: %v", err)
	}
}

func TestUploadDonglePartitions(t *testing.T) {
	for _, part := range []struct {
		name string
		size int
	}{{"fsc", 1024}, {"fsg", 1572864}, {"modemst1", 1572864}, {"modemst2", 1572864}} {
		t.Run(part.name, func(t *testing.T) {
			data := bytes.Repeat([]byte{0x13, 0x37, 0xab, 0xcd}, part.size/4)
			steps := []bulkStep{out("oem dump " + part.name), in("OKAY"), out("upload"), in("INFOstaged"), in(fmt.Sprintf("DATA%08x", part.size))}
			for offset, count := 0, 0; offset < len(data); count++ {
				n := min([]int{1, 0, 511, 16381, 65536}[count%5], len(data)-offset)
				steps = append(steps, bulkStep{data: data[offset : offset+n], callerOnly: true})
				offset += n
			}
			steps = append(steps, in("TEXTfinished"), in("OKAY"))
			c, _ := script(t, steps...)
			if _, err := c.OEM(t.Context(), "dump "+part.name); err != nil {
				t.Fatal(err)
			}
			var dst bytes.Buffer
			var last Event
			n, err := c.Upload(context.Background(), &dst, func(e Event) { last = e })
			if err != nil || n != int64(part.size) || !bytes.Equal(dst.Bytes(), data) {
				t.Fatalf("upload: %d bytes, %v", n, err)
			}
			if last.Phase != "receiving" || last.Done != n || last.Total != n || !reflect.DeepEqual(last.Info, []string{"staged", "finished"}) {
				t.Fatalf("upload progress: %+v", last)
			}
		})
	}
}

type shortWriter struct{}

func (shortWriter) Write(p []byte) (int, error) { return len(p) - 1, nil }

func TestUploadErrors(t *testing.T) {
	c, _ := script(t, out("upload"), in("INFObefore"), in("DATA00000004"), bulkStep{data: []byte("abcd")}, in("TEXTafter"), in("FAILdump failed"))
	var dst bytes.Buffer
	n, err := c.Upload(t.Context(), &dst, nil)
	var fail *FailError
	if n != 4 || !errors.As(err, &fail) || !reflect.DeepEqual(fail.Info, []string{"before", "after"}) {
		t.Fatalf("upload final FAIL: %d, %v", n, err)
	}
	for _, reply := range []string{"OKAY", "FAILno staged data", "DATAinvalid!"} {
		c, _ := script(t, out("upload"), in(reply))
		if n, err := c.Upload(t.Context(), io.Discard, nil); n != 0 || err == nil {
			t.Fatalf("upload %s: %d, %v", reply, n, err)
		}
	}
	c, _ = script(t, out("upload"), in("DATA00000004"), bulkStep{data: []byte("abcd")})
	if n, err := c.Upload(t.Context(), shortWriter{}, nil); n != 3 || !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("short writer: %d, %v", n, err)
	}
	if err := c.Reboot(t.Context()); !errors.Is(err, ErrBroken) {
		t.Fatalf("after failed writer: %v", err)
	}
	c, _ = script(t, out("upload"), in("DATA00000000"), in("OKAY"))
	if n, err := c.Upload(t.Context(), io.Discard, nil); n != 0 || err != nil {
		t.Fatalf("empty upload: %d, %v", n, err)
	}
}

func TestTimeoutsBreakClient(t *testing.T) {
	for _, tc := range []struct {
		name     string
		steps    []bulkStep
		run      func(*Client) error
		duration time.Duration
	}{
		{"getvar", []bulkStep{out("getvar:product"), {stall: true, buffer: 256, deadline: commandTimeout}}, func(c *Client) error { _, err := c.GetVar(context.Background(), "product"); return err }, commandTimeout},
		{"oem", []bulkStep{out("oem dump fsc"), {stall: true, buffer: 256, deadline: commandTimeout}}, func(c *Client) error { _, err := c.OEM(context.Background(), "dump fsc"); return err }, commandTimeout},
		{"erase", []bulkStep{out("erase:rootfs"), {stall: true, buffer: 256, deadline: flashTimeout}}, func(c *Client) error { return c.Erase(context.Background(), "rootfs") }, flashTimeout},
		{"download status", []bulkStep{out("download:00000000"), in("DATA00000000"), {stall: true, buffer: 256, deadline: commandTimeout}}, func(c *Client) error { return c.Download(context.Background(), strings.NewReader(""), 0, nil) }, commandTimeout},
		{"upload status", []bulkStep{out("upload"), in("DATA00000000"), {stall: true, buffer: 256, deadline: commandTimeout}}, func(c *Client) error { _, err := c.Upload(context.Background(), io.Discard, nil); return err }, commandTimeout},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				c, _ := script(t, tc.steps...)
				start := time.Now()
				err := tc.run(c)
				var op *OpError
				if !errors.As(err, &op) || !errors.Is(err, context.DeadlineExceeded) || time.Since(start) != tc.duration {
					t.Fatalf("timeout after %v: %v", time.Since(start), err)
				}
				if err := c.Reboot(context.Background()); !errors.Is(err, ErrBroken) {
					t.Fatalf("after timeout: %v", err)
				}
				if err := c.Close(); err != nil {
					t.Fatal(err)
				}
				if err := c.Reboot(context.Background()); !errors.Is(err, ErrClosed) {
					t.Fatalf("after close: %v", err)
				}
			})
		})
	}
}

func TestCancellationAndTransferErrors(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// The data phase outlives the 10 s handshake timeout and uses the caller's 20 s deadline.
		c, _ := script(t, out("download:00000004"), in("DATA00000004"), bulkStep{out: true, data: []byte("abcd"), stall: true})
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		start := time.Now()
		err := c.Download(ctx, strings.NewReader("abcd"), 4, nil)
		if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) != 20*time.Second {
			t.Fatalf("data deadline after %v: %v", time.Since(start), err)
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c, _ := script(t, out("reboot"), in("OKAY"))
	if _, err := c.GetVar(ctx, "product"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := Open(ctx, usb.DeviceInfo{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	} // no USB open
	if err := c.Reboot(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, step := range []bulkStep{{out: true, data: []byte("reboot"), short: true}, {out: true, data: []byte("reboot"), err: io.ErrClosedPipe}} {
		c, _ := script(t, step)
		var op *OpError
		if err := c.Reboot(t.Context()); !errors.As(err, &op) {
			t.Fatalf("write failure: %v", err)
		}
		if err := c.Reboot(t.Context()); !errors.Is(err, ErrBroken) {
			t.Fatalf("after write failure: %v", err)
		}
	}
}

type readerFunc func([]byte) (int, error)

func (r readerFunc) Read(p []byte) (int, error) { return r(p) }

func TestDownloadReaderCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, _ := script(t, out("download:00000004"), in("DATA00000004"))
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		reads := 0
		r := readerFunc(func([]byte) (int, error) {
			reads++
			<-ctx.Done()
			return 0, nil // A short source read must not hide cancellation inside io.ReadFull.
		})
		err := c.Download(ctx, r, 4, nil)
		if !errors.Is(err, context.DeadlineExceeded) || reads != 1 {
			t.Fatalf("source cancellation after %d reads: %v", reads, err)
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c, _ := script(t, out("download:00000004"), in("DATA00000004"))
	err := c.Download(ctx, strings.NewReader("abcd"), 4, func(Event) { cancel() })
	var op *OpError
	if !errors.As(err, &op) || !errors.Is(err, context.Canceled) {
		t.Fatalf("in-flight cancellation: %v", err)
	}
	if err := c.Reboot(t.Context()); !errors.Is(err, ErrBroken) {
		t.Fatalf("after cancellation: %v", err)
	}

	step := payload([]byte("abcd"))
	step.short = true
	c, _ = script(t, out("download:00000004"), in("INFOready"), in("DATA00000004"), step)
	err = c.Download(context.Background(), strings.NewReader("abcd"), 4, nil)
	if !errors.As(err, &op) || !errors.Is(err, io.ErrShortWrite) || !reflect.DeepEqual(op.Info, []string{"ready"}) {
		t.Fatalf("data write failure lost INFO: %v", err)
	}
}

func TestUploadDataTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, _ := script(t, out("upload"), in("DATA00000004"), bulkStep{stall: true})
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		start := time.Now()
		n, err := c.Upload(ctx, io.Discard, nil)
		var op *OpError
		if n != 0 || !errors.As(err, &op) || !errors.Is(err, context.DeadlineExceeded) || time.Since(start) != 20*time.Second {
			t.Fatalf("upload data timeout after %v: %d, %v", time.Since(start), n, err)
		}
		if err := c.Reboot(t.Context()); !errors.Is(err, ErrBroken) {
			t.Fatalf("after upload data timeout: %v", err)
		}
	})
}

func TestSerialisationAndWaitingCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		gate := make(chan struct{})
		last := in("OKAYfirst")
		last.gate = gate
		c, _ := script(t, out("oem first"), last, out("getvar:product"), in("OKAYMSM8916"))
		done := make(chan error, 1)
		go func() { _, err := c.OEM(context.Background(), "first"); done <- err }()
		synctest.Wait()
		ctx, cancel := context.WithCancel(context.Background())
		waiter := make(chan error, 1)
		go func() { _, err := c.GetVar(ctx, "product"); waiter <- err }()
		synctest.Wait()
		cancel()
		if err := <-waiter; !errors.Is(err, context.Canceled) {
			t.Fatalf("waiting cancellation: %v", err)
		}
		close(gate)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if got, err := c.GetVar(context.Background(), "product"); err != nil || got != "MSM8916" {
			t.Fatalf("after waiting cancellation: %q, %v", got, err)
		}
	})
}
