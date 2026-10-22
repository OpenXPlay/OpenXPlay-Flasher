// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package base

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/adb"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/catalog"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/device"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/fastboot"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/profile"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/stage"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/testgpt"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/usb"
)

func hash(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }
func put(t *testing.T, path string, b []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

type testArchives map[string]string

func (a testArchives) Resolve(_ context.Context, role string, _ profile.Archive, _ catalog.ProgressFunc) (string, string, error) {
	return a[role], "local", nil
}

type fixture struct {
	p        profile.Profile
	files    *Files
	store    *Store
	journals Journals
	archives testArchives
	dumps    map[string][]byte
}

func makeFixture(t *testing.T) fixture {
	t.Helper()
	ps, err := profile.Builtin()
	if err != nil {
		t.Fatal(err)
	}
	p := ps[0]
	b := p.Base
	root := t.TempDir()
	paths := testArchives{}
	bodies := map[string]map[string][]byte{"base-generic": {}, "rootfs-zip": {}}
	for id, pin := range b.Files {
		data := []byte("pinned-image-" + id)
		if id == "gpt" {
			data = testgpt.Base()
		}
		if id == "tz" {
			data = bytes.Repeat([]byte{0x74}, 605312)
		}
		// Small raw fixtures exercise sequencing; fastboot's sparse tests cover actual 200 MiB pieces.
		pin.Size = int64(len(data))
		pin.SHA256 = hash(data)
		b.Files[id] = pin
		if pin.Member != "" {
			bodies[pin.Archive][pin.Member] = data
		} else {
			path := filepath.Join(root, "board-boot")
			put(t, path, data)
			paths[pin.Archive] = path
			a := b.Archives[pin.Archive]
			a.Size = pin.Size
			a.SHA256 = pin.SHA256
			b.Archives[pin.Archive] = a
		}
	}
	dumps := map[string][]byte{}
	for i, name := range partitionNames {
		b.Protected[name] = []int64{64}
		author := bytes.Repeat([]byte{byte(21 + i)}, 64)
		bodies["base-generic"][b.NeverWrite[name].Member] = author
		dumps[name] = bytes.Repeat([]byte{byte(91 + i)}, 64)
	}
	dumps["fsc"] = make([]byte, 64)
	for role, entries := range bodies {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		for name, data := range entries {
			w, err := zw.Create(name)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = w.Write(data); err != nil {
				t.Fatal(err)
			}
		}
		if err = zw.Close(); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(root, role+".zip")
		put(t, path, buf.Bytes())
		paths[role] = path
		a := b.Archives[role]
		a.Size = int64(buf.Len())
		a.SHA256 = hash(buf.Bytes())
		b.Archives[role] = a
	}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	files := &Files{Dir: filepath.Join(root, "cache"), Profile: p, Provider: paths}
	if err := files.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	store := &Store{Dir: filepath.Join(root, "backups"), Profile: p, Version: "test", Author: files.AuthorHashes()}
	return fixture{p, files, store, Journals{Dir: filepath.Join(root, "data")}, paths, dumps}
}
func (f fixture) capture(t *testing.T, serial string) Backup {
	t.Helper()
	b, err := f.store.Capture(context.Background(), serial, "oem-dump", func(_ context.Context, name string, w io.Writer) error { _, err := w.Write(f.dumps[name]); return err })
	if err != nil {
		t.Fatal(err)
	}
	return b.Backup
}

func TestEveryPlanSequence(t *testing.T) {
	f := makeFixture(t)
	bootstrap := []string{"flash:aboot", "erase:boot", "reboot", "wait-fastboot"}
	backup := []string{"backup"}
	chain := []string{"erase:boot", "reboot-bootloader", "wait-fastboot", "flash:partition", "flash:hyp", "flash:rpm", "flash:sbl1", "flash:tz", "restore:fsc", "restore:fsg", "restore:modemst1", "restore:modemst2", "flash:aboot", "flash:cdt", "erase:boot", "erase:rootfs", "reboot", "wait-fastboot"}
	system := []string{"flash:rootfs", "flash:boot", "reboot", "wait-linux"}
	concat := func(parts ...[]string) []string {
		out := []string{}
		for _, p := range parts {
			out = append(out, p...)
		}
		return out
	}
	for _, tc := range []struct {
		name, kind string
		mode       profile.Mode
		product    string
		want       []string
	}{
		{"stock adb", "stock", profile.ModeStock, "", concat([]string{"confirm-stock", "adb-bootloader", "wait-fastboot"}, bootstrap, backup, chain, system)},
		{"stock fastboot", "stock", profile.ModeFastboot, f.p.Base.Fastboot.StockProduct, concat(bootstrap, backup, chain, system)},
		// A system reinstall erases boot first, so an interrupted rootfs write ends in lk1st's fastboot.
		{"system adb", "system", profile.ModeADB, "", concat([]string{"confirm-linux", "adb-bootloader", "wait-fastboot"}, backup, []string{"erase:boot"}, system)},
		{"system fastboot", "system", profile.ModeFastboot, f.p.Base.Fastboot.Product, concat(backup, []string{"erase:boot"}, system)},
		{"repair lk1st", "repair", profile.ModeFastboot, f.p.Base.Fastboot.Product, concat(backup, chain, system)},
		{"repair stock", "repair", profile.ModeFastboot, f.p.Base.Fastboot.StockProduct, concat(bootstrap, backup, chain, system)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan := BuildPlan(f.p, tc.kind, Identity{Mode: tc.mode, Product: tc.product, Serial: "board1", Location: "1-1"})
			if plan.Problem != nil {
				t.Fatal(plan.Problem)
			}
			var got []string
			for _, s := range plan.Steps {
				a := s.Action
				if s.Partition != "" {
					a += ":" + s.Partition
				}
				got = append(got, a)
				if s.Critical == s.Resumable || s.Name["en"] == "" || s.Name["zh-CN"] == "" {
					t.Fatalf("bad phase metadata: %+v", s)
				}
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v\nwant %v", got, tc.want)
			}
			inChain := false
			for _, s := range plan.Steps {
				if s.ID == "chain-partition" {
					inChain = true
				}
				if inChain && s.Window != "chain" {
					t.Fatalf("pause in critical window: %s", s.ID)
				}
				if s.ID == "chain-reboot" {
					inChain = false
				}
			}
		})
	}
}

func TestExtractionPinsReuseAndNeverWrite(t *testing.T) {
	f := makeFixture(t)
	ctx := context.Background()
	for _, name := range partitionNames {
		if _, err := f.files.Open(ctx, name); err == nil {
			t.Fatal("opened protected ID", name)
		}
		if _, err := f.files.Open(ctx, f.p.Base.NeverWrite[name].Member); err == nil {
			t.Fatal("opened author member")
		}
	}
	root := filepath.Join(f.files.Dir, "base", f.p.Base.Release)
	for _, name := range partitionNames {
		if _, err := os.Stat(filepath.Join(root, name+".bin")); !os.IsNotExist(err) {
			t.Fatal("author file extracted")
		}
	}
	path := f.files.paths["rootfs"]
	before, _ := os.Stat(path)
	if err := f.files.Prepare(ctx); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(path)
	if !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("rewrote verified extraction")
	}
	put(t, path, []byte("tampered"))
	if _, err := f.files.Open(ctx, "rootfs"); err == nil {
		t.Fatal("changed member accepted")
	}
	if err := f.files.Prepare(ctx); err != nil {
		t.Fatal(err)
	}
	pin := f.p.Base.Files["hyp"]
	pin.SHA256 = strings.Repeat("a", 64)
	f.p.Base.Files["hyp"] = pin
	bad := &Files{Dir: t.TempDir(), Profile: f.p, Provider: f.archives}
	if err := bad.Prepare(ctx); !errors.Is(err, catalog.ErrIntegrity) {
		t.Fatalf("wrong member pin: %v", err)
	}
}

func TestBackupValidationImportAndSets(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(f fixture)
		want   string
	}{
		{"fsc zero", func(f fixture) {}, ""},
		{"fsg zero", func(f fixture) { f.dumps["fsg"] = make([]byte, 64) }, "backup-empty"},
		{"modem ff", func(f fixture) { f.dumps["modemst1"] = bytes.Repeat([]byte{255}, 64) }, "backup-empty"},
		{"author", func(f fixture) { f.dumps["fsg"] = bytes.Repeat([]byte{22}, 64) }, "backup-author"},
		{"size", func(f fixture) { f.dumps["fsg"] = []byte{1} }, "backup-size"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := makeFixture(t)
			tc.change(f)
			_, err := f.store.Capture(t.Context(), "board1", "oem-dump", func(_ context.Context, name string, w io.Writer) error { _, err := w.Write(f.dumps[name]); return err })
			var p *stage.Problem
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if !errors.As(err, &p) || p.Code != tc.want {
				t.Fatalf("got %v want %s", err, tc.want)
			}
		})
	}
	f := makeFixture(t)
	first := f.capture(t, "board1")
	same := f.capture(t, "board1")
	if first.Set != same.Set || first.Created != same.Created {
		t.Fatal("overwrote valid backup")
	}
	f.dumps["fsg"][0]++
	changed, err := f.store.Capture(t.Context(), "board1", "oem-dump", func(_ context.Context, name string, w io.Writer) error { _, err := w.Write(f.dumps[name]); return err })
	if err != nil || changed.Set == first.Set || !slices.Equal(changed.Differing, []string{"fsg"}) {
		t.Fatal(err)
	}
	kept, err := f.store.Validate(t.Context(), "board1", first.Set)
	if err != nil || kept.Partitions["fsg"] != first.Partitions["fsg"] {
		t.Fatal("existing backup changed", err)
	}
	dir, _ := f.store.setPath("board1", first.Set)
	imported := &Store{Dir: t.TempDir(), Profile: f.p, Author: f.store.Author}
	b, err := imported.Import(t.Context(), dir, "board1")
	if err != nil || b.Partitions["fsg"].Source != "imported" {
		t.Fatal(err)
	}
	put(t, filepath.Join(dir, "modemst2.bin"), bytes.Repeat([]byte{72}, 64))
	if _, err := f.store.Validate(t.Context(), "board1", first.Set); err == nil {
		t.Fatal("backup manifest hash ignored")
	}
	if _, err := imported.OpenRestore(t.Context(), "other-board", b.Set, "fsg"); err == nil {
		t.Fatal("opened another serial's backup")
	}
}

type fakeDetector struct {
	candidates []device.Candidate
	onScan     func()
}

func (d *fakeDetector) Scan(context.Context) device.Snapshot {
	if d.onScan != nil {
		d.onScan()
	}
	return device.Snapshot{Candidates: slices.Clone(d.candidates)}
}
func fbCandidate(address int) device.Candidate {
	return device.Candidate{Mode: profile.ModeFastboot, USB: usb.DeviceInfo{Bus: 1, Address: address, Path: []int{address}, VendorID: 0x18d1, ProductID: 0xd00d}, Profiles: []string{"ufi003"}}
}
func linuxCandidate() device.Candidate {
	return device.Candidate{Mode: profile.ModeADB, USB: usb.DeviceInfo{Bus: 1, Address: 1, Path: []int{1}, VendorID: 0x18d1, ProductID: 0xd001}, Profiles: []string{"ufi003"}}
}

type fakeFB struct {
	f               fixture
	det             *fakeDetector
	serial, product string
	calls           []string
	dump            string
	onWrite         func(string)
	fail            string
	expectRestores  map[string]Partition
	restored        []string
	boot            bool
	badContext      bool
	broken          bool
	stockSizes      map[string]int64
	sizeQueries     []string
	sizeOptions     map[string][]int64
}

func (f *fakeFB) GetVar(_ context.Context, name string) (string, error) {
	if f.broken {
		return "", fastboot.ErrBroken
	}
	if name == "product" {
		return f.product, nil
	}
	if name == "serialno" {
		return f.serial, nil
	}
	if partition, ok := strings.CutPrefix(name, "partition-size:"); ok {
		f.sizeQueries = append(f.sizeQueries, partition)
		if size, ok := f.stockSizes[partition]; ok {
			return fmt.Sprintf("0x%x", size), nil
		}
	}
	return "", nil
}
func (f *fakeFB) write(ctx context.Context, command string) error {
	if ctx.Err() != nil {
		f.badContext = true
		return ctx.Err()
	}
	f.calls = append(f.calls, command)
	if f.onWrite != nil {
		f.onWrite(command)
	}
	if f.fail == command {
		return errors.New("injected transport failure")
	}
	return nil
}
func (f *fakeFB) Flash(ctx context.Context, partition string, img *fastboot.Image, opts fastboot.FlashOptions, _ fastboot.Progress) error {
	if opts.MaxPiece != 200<<20 {
		return errors.New("wrong piece limit")
	}
	if f.sizeOptions == nil {
		f.sizeOptions = map[string][]int64{}
	}
	f.sizeOptions[partition] = append(f.sizeOptions[partition], opts.PartitionSize)
	capacity := opts.PartitionSize
	if capacity == 0 {
		if _, err := f.GetVar(ctx, "partition-size:"+partition); err != nil {
			return err
		}
		capacity = f.stockSizes[partition]
	}
	if capacity > 0 && img.Size() > capacity {
		return &fastboot.PartitionSizeError{Partition: partition, ImageSize: img.Size(), PartitionSize: capacity}
	}
	if opts.BeforeWrite != nil {
		if err := opts.BeforeWrite(ctx); err != nil {
			return err
		}
	}
	if protected(partition) {
		if pin, ok := f.expectRestores[partition]; ok {
			if err := img.Verify(ctx, pin.Size, pin.SHA256); err != nil {
				return fmt.Errorf("restored wrong %s: %w", partition, err)
			}
		}
		f.restored = append(f.restored, partition)
	}
	if partition == "boot" {
		f.boot = true
	}
	return f.write(ctx, "flash:"+partition)
}
func (f *fakeFB) Erase(ctx context.Context, partition string) error {
	if partition == "boot" {
		f.boot = false
	}
	return f.write(ctx, "erase:"+partition)
}
func (f *fakeFB) OEM(_ context.Context, arg string) (fastboot.Response, error) {
	f.dump = strings.TrimPrefix(arg, "dump ")
	f.calls = append(f.calls, "oem "+arg)
	return fastboot.Response{Info: []string{"sensitive data must never be logged"}}, nil
}
func (f *fakeFB) Upload(_ context.Context, w io.Writer, _ fastboot.Progress) (int64, error) {
	f.calls = append(f.calls, "upload")
	n, err := w.Write(f.f.dumps[f.dump])
	if err != nil {
		f.broken = true
	}
	return int64(n), err
}
func (f *fakeFB) Reboot(ctx context.Context) error {
	err := f.write(ctx, "reboot")
	if f.product == f.f.p.Base.Fastboot.StockProduct {
		f.product = f.f.p.Base.Fastboot.Product
	}
	if f.boot {
		f.det.candidates = []device.Candidate{linuxCandidate()}
	} else {
		f.det.candidates[0].USB.Address++
	}
	return err
}
func (f *fakeFB) RebootBootloader(ctx context.Context) error {
	err := f.write(ctx, "reboot-bootloader")
	if err == nil {
		f.det.candidates[0].USB.Address++
	}
	return err
}
func (f *fakeFB) Close() error { return nil }

type fakeADB struct {
	f      fixture
	fb     *fakeFB
	serial string
	calls  []string
	stock  bool
}

func (d *fakeADB) Shell(_ context.Context, cmd string) (adb.Result, error) {
	d.calls = append(d.calls, cmd)
	out := ""
	switch {
	case cmd == "cat /proc/cmdline":
		out = "console=tty0 androidboot.serialno=" + d.serial
	case strings.HasPrefix(cmd, "getprop "):
		key := strings.TrimPrefix(cmd, "getprop ")
		out = d.f.p.Base.Stock.Properties[key]
		if key == "ro.serialno" {
			out = d.serial
		}
		if key == "ro.build.version.release" {
			out = "4.4.4"
		}
	case cmd == "reboot bootloader":
		d.fb.det.candidates = []device.Candidate{fbCandidate(1)}
	case strings.Contains(cmd, "uname -r"):
		out = "root\t0\nkernel\t5.15.0-handsomekernel+\nmodel\tHandsome Openstick\ndebian\t11\narch\tarm64\n"
	case strings.HasPrefix(cmd, "dpkg-query"):
		return adb.Result{ExitCode: 1}, nil
	case strings.Contains(cmd, "df -Pk"):
		out = "root 1000000 10000 990000 1% /"
	case cmd == "df -h /":
		out = "root 4G 1G 3G /"
	case cmd == "systemctl --failed --no-pager":
		out = "0 loaded units listed."
	default:
		return adb.Result{}, errors.New("unexpected fake shell: " + cmd)
	}
	return adb.Result{Stdout: []byte(out)}, nil
}
func (d *fakeADB) Serial() string { return d.serial }
func (d *fakeADB) Pull(context.Context, string, int64) ([]byte, error) {
	return nil, errors.New("unexpected pull")
}
func (d *fakeADB) Push(context.Context, string, []byte, fs.FileMode) error {
	return errors.New("unexpected push")
}
func (d *fakeADB) Close() error { return nil }

type fakeConnector struct {
	d     *fakeADB
	opens int
}

func (c *fakeConnector) Connect(context.Context, usb.DeviceInfo) (adb.Device, error) {
	c.opens++
	return c.d, nil
}

func executorFixture(t *testing.T, kind string, mode profile.Mode, product string) (*Executor, Plan, *fakeFB, *fakeDetector) {
	t.Helper()
	f := makeFixture(t)
	det := &fakeDetector{candidates: []device.Candidate{fbCandidate(1)}}
	fb := &fakeFB{f: f, det: det, serial: "board1", product: product}
	d := &fakeADB{f: f, fb: fb, serial: "board1"}
	if mode == profile.ModeADB {
		det.candidates = []device.Candidate{linuxCandidate()}
	}
	if mode == profile.ModeStock {
		c := linuxCandidate()
		c.Mode = profile.ModeStock
		det.candidates = []device.Candidate{c}
	}
	e := NewExecutor(f.p, f.files, f.store, f.journals)
	e.Detector = det
	e.Open = func(context.Context, usb.DeviceInfo) (Fastboot, error) { fb.broken = false; return fb, nil }
	e.ADB = &fakeConnector{d: d}
	e.PollInterval = time.Millisecond
	e.FastbootTimeout = 20 * time.Millisecond
	e.LinuxTimeout = 20 * time.Millisecond
	return e, BuildPlan(f.p, kind, Identity{Mode: mode, Product: product, Serial: "board1", Location: "1-1"}), fb, det
}

func TestExecutorEveryEntryAndJournal(t *testing.T) {
	for _, tc := range []struct {
		kind    string
		mode    profile.Mode
		product string
	}{{"stock", profile.ModeStock, "MSM8916"}, {"stock", profile.ModeFastboot, "MSM8916"}, {"system", profile.ModeADB, "LK1ST_MSM8916"}, {"system", profile.ModeFastboot, "LK1ST_MSM8916"}, {"repair", profile.ModeFastboot, "LK1ST_MSM8916"}, {"repair", profile.ModeFastboot, "MSM8916"}} {
		t.Run(tc.kind+string(tc.mode)+tc.product, func(t *testing.T) {
			e, plan, fb, _ := executorFixture(t, tc.kind, tc.mode, tc.product)
			job := e.Run(t.Context(), plan)
			if job.Status != "succeeded" || !job.Observed || job.Findings["backupSet"] == "" || job.Findings["df -h /"] == "" || job.Findings["systemctl --failed --no-pager"] == "" {
				t.Fatalf("job=%+v", job)
			}
			j, err := e.Journals.Load(t.Context(), e.Profile.ID, plan.Serial)
			if err != nil || j == nil || !j.Finished || len(j.Completed) != len(plan.Steps) {
				t.Fatalf("journal=%+v err=%v", j, err)
			}
			if strings.Contains(job.Log, "sensitive") {
				t.Fatal("OEM data leaked")
			}
			if tc.kind == "system" && slices.Contains(fb.calls, "flash:partition") {
				t.Fatal("system rewrote chain")
			}
			if tc.product == "MSM8916" && slices.Index(fb.calls, "flash:aboot") >= slices.Index(fb.calls, "erase:boot") {
				t.Fatal("stock order reversed")
			}
		})
	}
}

func TestGuardsStopBeforeWrites(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*fakeFB, *fakeDetector)
		want   string
	}{
		{"serial", func(f *fakeFB, _ *fakeDetector) { f.serial = "other-board" }, "serial-mismatch"},
		{"product", func(f *fakeFB, _ *fakeDetector) { f.product = "phone" }, "wrong-product"},
		{"second fastboot", func(_ *fakeFB, d *fakeDetector) { d.candidates = append(d.candidates, fbCandidate(2)) }, "several-devices"},
		{"second family", func(_ *fakeFB, d *fakeDetector) {
			c := linuxCandidate()
			c.USB.Path = []int{2}
			d.candidates = append(d.candidates, c)
		}, "several-devices"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, p, fb, d := executorFixture(t, "repair", profile.ModeFastboot, "LK1ST_MSM8916")
			tc.change(fb, d)
			j := e.Run(t.Context(), p)
			if j.Problem == nil || j.Problem.Code != tc.want || len(fb.calls) != 0 {
				t.Fatalf("job=%+v calls=%v", j, fb.calls)
			}
		})
	}
	e, p, fb, d := executorFixture(t, "system", profile.ModeFastboot, "LK1ST_MSM8916")
	fb.onWrite = func(cmd string) {
		if cmd == "flash:rootfs" {
			d.candidates = append(d.candidates, fbCandidate(2))
		}
	}
	j := e.Run(t.Context(), p)
	if j.Problem.Code != "several-devices" || slices.Contains(fb.calls, "flash:boot") {
		t.Fatalf("second device did not stop next write: %v", fb.calls)
	}
}

func TestProtectedPolicyIndependentOfPlan(t *testing.T) {
	e, plan, fb, _ := executorFixture(t, "repair", profile.ModeFastboot, "LK1ST_MSM8916")
	e.serial = plan.Serial
	e.Store.Author = e.Files.AuthorHashes()
	for _, action := range []string{"flash", "erase"} {
		for _, name := range partitionNames {
			if err := e.write(t.Context(), Step{Action: action, Partition: name, FileID: name, Product: fb.product}); err == nil {
				t.Fatal("protected write allowed", action, name)
			}
		}
	}
	if len(fb.calls) != 0 {
		t.Fatal("policy allowed commands")
	}
	if err := e.write(t.Context(), Step{Action: "flash", Partition: "boot", FileID: "base/fsg.bin", Product: fb.product}); err == nil {
		t.Fatal("neverWrite allowed")
	}
	if err := e.write(t.Context(), Step{Action: "restore", Partition: "fsg", Product: fb.product}); err == nil {
		t.Fatal("restore without matching backup")
	}
	fb.f.capture(t, "board1")
	fb.serial = "other-board"
	if err := e.write(t.Context(), Step{Action: "restore", Partition: "fsg", Product: fb.product}); err == nil {
		t.Fatal("restore to other board")
	}
	if len(fb.calls) != 0 {
		t.Fatal("invalid restore reached flash")
	}
}

func TestCriticalCancellationFailureAndResume(t *testing.T) {
	e, p, fb, _ := executorFixture(t, "repair", profile.ModeFastboot, "LK1ST_MSM8916")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fb.onWrite = func(cmd string) {
		if cmd == "flash:partition" {
			cancel()
		}
	}
	j := e.Run(ctx, p)
	for _, cmd := range []string{"flash:hyp", "flash:rpm", "flash:sbl1", "flash:tz", "flash:fsc", "flash:fsg", "flash:modemst1", "flash:modemst2", "flash:aboot", "flash:cdt", "erase:rootfs", "reboot"} {
		if !slices.Contains(fb.calls, cmd) {
			t.Fatal("cancellation interrupted critical window", cmd, fb.calls)
		}
	}
	if fb.badContext || j.Status != "failed" || slices.Contains(fb.calls, "flash:rootfs") {
		t.Fatalf("job=%+v calls=%v", j, fb.calls)
	}
	journal, err := e.Journals.Load(t.Context(), e.Profile.ID, p.Serial)
	if err != nil || ResumeChoice(journal) != "repair" {
		t.Fatal("missing resume offer", err)
	}
	e, p, fb, _ = executorFixture(t, "repair", profile.ModeFastboot, "LK1ST_MSM8916")
	fb.fail = "flash:rpm"
	j = e.Run(t.Context(), p)
	if j.Left != "partial" || j.Uncertain != "chain-rpm" || !slices.Contains(j.Written, "chain-hyp") || slices.Contains(fb.calls, "flash:sbl1") {
		t.Fatalf("failure lost precise state: %+v", j)
	}
	if count := len(slices.Collect(func(yield func(string) bool) {
		for _, c := range fb.calls {
			if c == fb.fail {
				if !yield(c) {
					return
				}
			}
		}
	})); count != 1 {
		t.Fatal("critical step retried")
	}
	journal, err = e.Journals.Load(t.Context(), e.Profile.ID, p.Serial)
	if err != nil || journal.InFlight != "chain-rpm" || journal.Finished {
		t.Fatal("failure intent lost", err)
	}
}

func TestReconnectChecksIdentityAndUnknownADBIsNeverOpened(t *testing.T) {
	e, p, fb, d := executorFixture(t, "system", profile.ModeFastboot, "LK1ST_MSM8916")
	// Unknown phones are ignored. Only the known Linux candidate may be opened after boot.
	phone := device.Candidate{Mode: profile.ModeADB, USB: usb.DeviceInfo{Bus: 2, Address: 3, VendorID: 0x1234, ProductID: 0xabcd}}
	d.candidates = append(d.candidates, phone)
	opens := 0
	orig := e.Open
	e.Open = func(ctx context.Context, info usb.DeviceInfo) (Fastboot, error) { opens++; return orig(ctx, info) }
	fb.onWrite = func(cmd string) {
		if cmd == "flash:rootfs" {
			d.candidates[0].USB.Address++
			fb.serial = "wrong-after-reconnect"
		}
	}
	j := e.Run(t.Context(), p)
	if j.Problem.Code != "serial-mismatch" || slices.Contains(fb.calls, "flash:boot") || opens != 2 {
		t.Fatalf("reconnect not guarded: opens %d calls %v job %+v", opens, fb.calls, j)
	}
}

func TestExistingDifferentBackupContinues(t *testing.T) {
	e, p, fb, _ := executorFixture(t, "repair", profile.ModeFastboot, "LK1ST_MSM8916")
	fb.f.capture(t, "board1")
	fb.f.dumps["modemst1"][0]++
	job := e.Run(t.Context(), p)
	if job.Status != "succeeded" || job.Findings["backupDiffering"] != "modemst1" || !slices.Contains(fb.calls, "flash:partition") {
		t.Fatalf("changed EFS stopped job: %+v %v", job, fb.calls)
	}
}

func TestFSCZeroMayEqualAuthorAndUnrecognizedFastbootIDsAreAccepted(t *testing.T) {
	f := makeFixture(t)
	f.store.Author["fsc"] = AuthorFile{Size: int64(len(f.dumps["fsc"])), SHA256: hash(f.dumps["fsc"])}
	b := f.capture(t, "board1")
	if _, err := f.store.Validate(t.Context(), "board1", b.Set); err != nil {
		t.Fatal("zero fsc exception rejected", err)
	}
	e, p, _, d := executorFixture(t, "system", profile.ModeFastboot, "LK1ST_MSM8916")
	d.candidates[0].Profiles = nil
	d.candidates[0].USB.VendorID = 0x1234
	d.candidates[0].USB.ProductID = 0xabcd
	if job := e.Run(t.Context(), p); job.Status != "succeeded" {
		t.Fatalf("matched getvars refused for unrecognized VID:PID: %+v", job)
	}
}

func TestInvalidPlanAndUnknownADBWaitNeverSendCommands(t *testing.T) {
	e, p, fb, d := executorFixture(t, "repair", profile.ModeFastboot, "LK1ST_MSM8916")
	p.Steps[0].Action = "flash"
	p.Steps[0].Partition = "fsg"
	p.Steps[0].FileID = "fsg"
	if job := e.Run(t.Context(), p); job.Problem.Code != "bad-plan" || len(fb.calls) != 0 {
		t.Fatal("mutated plan accepted")
	}
	e.serial = "board1"
	d.candidates = []device.Candidate{{Mode: profile.ModeADB, USB: usb.DeviceInfo{VendorID: 0x1111, ProductID: 0x2222}}}
	connector := e.ADB.(*fakeConnector)
	before := connector.opens
	if err := e.waitLinux(t.Context()); err == nil || connector.opens != before {
		t.Fatal("unknown adb probed during Linux wait")
	}
	e.FastbootTimeout = time.Millisecond
	if err := e.waitFastboot(t.Context(), fb.product); err == nil {
		t.Fatal("missing fastboot did not time out")
	}
}

func TestWaitForFastbootRejectsLingeringPreRebootConnection(t *testing.T) {
	for _, disappears := range []bool{false, true} {
		t.Run(fmt.Sprint(disappears), func(t *testing.T) {
			e, _, fb, det := executorFixture(t, "system", profile.ModeFastboot, "LK1ST_MSM8916")
			e.serial = "board1"
			if err := e.fastboot(t.Context(), fb.product); err != nil {
				t.Fatal(err)
			}
			old := *e.candidate
			e.reconnect = &old
			e.closeFastboot()
			scans := 0
			det.onScan = func() {
				scans++
				if scans == 2 && disappears {
					det.candidates = nil
				}
				if scans == 3 {
					det.candidates = []device.Candidate{old}
					if !disappears {
						det.candidates[0].USB.Address++
					}
				}
			}
			if err := e.waitFastboot(t.Context(), fb.product); err != nil || scans < 3 {
				t.Fatalf("accepted pre-reboot connection after %d scans: %v", scans, err)
			}
		})
	}
}

func TestBootstrapCancellationWaitsUntilReboot(t *testing.T) {
	e, plan, fb, _ := executorFixture(t, "stock", profile.ModeFastboot, "MSM8916")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fb.onWrite = func(cmd string) {
		if cmd == "flash:aboot" {
			cancel()
		}
	}
	job := e.Run(ctx, plan)
	if fb.badContext || job.Status != "failed" || !slices.Contains(fb.calls, "erase:boot") || !slices.Contains(fb.calls, "reboot") || slices.Contains(fb.calls, "oem dump fsc") {
		t.Fatalf("bootstrap cancellation left reboot incomplete: %+v %v", job, fb.calls)
	}
}

// The dongle re-enumerates for a moment after a reboot: a fastboot connection that flickers must
// not be opened until it has stayed on stablePolls consecutive scans.
func TestWaitFastbootNeedsAStableConnection(t *testing.T) {
	e, _, _, det := executorFixture(t, "repair", profile.ModeFastboot, "LK1ST_MSM8916")
	e.serial = "board1"
	e.FastbootTimeout = time.Second
	present := []bool{true, false, true, true, false, true, true, true}
	scans, opens, opensAt := 0, 0, -1
	det.onScan = func() {
		on := scans >= len(present) || present[scans]
		if on {
			det.candidates = []device.Candidate{fbCandidate(1)}
		} else {
			det.candidates = nil
		}
		scans++
	}
	orig := e.Open
	e.Open = func(ctx context.Context, info usb.DeviceInfo) (Fastboot, error) {
		opens++
		if opensAt < 0 {
			opensAt = scans
		}
		return orig(ctx, info)
	}
	if err := e.waitFastboot(t.Context(), "LK1ST_MSM8916"); err != nil {
		t.Fatal(err)
	}
	// Scans 1-5 never show three in a row; the open happens during the eighth wait scan.
	if opens != 1 || opensAt < len(present) {
		t.Fatalf("opens=%d at scan %d, want one open after scan %d", opens, opensAt, len(present))
	}
}
