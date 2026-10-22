// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-22

package cli

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/asn1"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/adb"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/app"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/stage"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/testssh"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/usb"
)

type nextDeviceFake struct {
	*cliDevice
	mu        sync.Mutex
	sizes     map[string]int
	commands  []string
	views     [][]byte
	private   bool
	restarted bool
	badStatus bool
	failWrite bool
	serial    string
	root      bool
}

func (d *nextDeviceFake) Shell(ctx context.Context, cmd string) (adb.Result, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.commands = append(d.commands, cmd)
	switch {
	case cmd == "cat /proc/cmdline":
		return adb.Result{Stdout: []byte("androidboot.serialno=" + d.serial)}, nil
	case strings.HasPrefix(cmd, "dpkg-query ") && strings.HasSuffix(cmd, "'openxplay-relay'") && d.version == "":
		return adb.Result{ExitCode: 1}, nil
	case strings.Contains(cmd, "stat -c '%s'"):
		if d.badStatus {
			return adb.Result{Stdout: []byte("unreadable fixture metadata")}, nil
		}
		return adb.Result{Stdout: []byte(fmt.Sprintf("%d\n%d\n", d.sizes["identity.pk8"], d.sizes["certificate.p7b"]))}, nil
	case strings.Contains(cmd, "/var/lib/openxplay/mfi"):
		if d.failWrite {
			return adb.Result{}, errors.New("private transport diagnostic must not escape")
		}
		if strings.Contains(cmd, "mkdir -m 700") && strings.Contains(cmd, "chmod 700") {
			d.private = true
		}
		if strings.Contains(cmd, "systemctl restart openxplay-relay.service") {
			if !strings.Contains(cmd, "chmod 600") || !strings.Contains(cmd, "chown 0:0") || !strings.Contains(cmd, "mv -f") {
				return adb.Result{}, errors.New("unsafe identity write")
			}
			d.restarted = true
		}
		return adb.Result{}, nil
	}
	r, err := d.cliDevice.Shell(ctx, cmd)
	if !d.root {
		r.Stdout = bytes.ReplaceAll(r.Stdout, []byte("root\t0"), []byte("root\t1000"))
	}
	return r, err
}
func (d *nextDeviceFake) Push(_ context.Context, p string, data []byte, mode fs.FileMode) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.private || !strings.Contains(p, "/mfi/.import-") || mode != 0o600 || len(data) == 0 || data[0] != 0x30 {
		return errors.New("unsafe identity upload")
	}
	d.sizes[filepath.Base(p)] = len(data)
	d.views = append(d.views, data)
	return nil
}
func (d *nextDeviceFake) OpenTCP(ctx context.Context, port int) (io.ReadWriteCloser, error) {
	if port != 6975 {
		return nil, errors.New("unexpected TCP port")
	}
	a, b := net.Pipe()
	go func() {
		defer b.Close()
		stop := context.AfterFunc(ctx, func() { b.Close() })
		defer stop()
		_, _ = io.Copy(b, b)
	}()
	return a, nil
}

type nextConnectorFake struct{ d *nextDeviceFake }

func (c nextConnectorFake) Connect(context.Context, usb.DeviceInfo) (adb.Device, error) {
	return c.d, nil
}

func nextFixture(t *testing.T) (*app.Service, *nextDeviceFake, *usb.FakeBus) {
	t.Helper()
	bus := usb.NewFakeBus(usb.DeviceInfo{VendorID: 0x18d1, ProductID: 0xd001, Bus: 1, Address: 1})
	svc, err := app.NewForBus(bus, quiet{})
	if err != nil {
		t.Fatal(err)
	}
	d := &nextDeviceFake{cliDevice: &cliDevice{version: "1.0"}, sizes: map[string]int{}, serial: "cli-board", root: true}
	svc.SetADB(nextConnectorFake{d})
	dir := t.TempDir()
	svc.ConfigureStorage(filepath.Join(dir, "data"), filepath.Join(dir, "cache"), filepath.Join(dir, "backups"), nil)
	previous := commandService
	commandService = func(bool, app.Events) (*app.Service, func(), error) {
		return svc, func() { svc.Stop() }, nil
	}
	t.Cleanup(func() { commandService = previous; svc.Stop(); bus.Close() })
	return svc, d, bus
}

// Generate disposable generic syntax fixtures, never real identity files.
func nextIdentityFiles(t *testing.T, format string) ([]string, [][]byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pk8, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	signed, err := asn1.Marshal(struct {
		Version int
		Digests []asn1.RawValue `asn1:"set"`
		Content struct{ Type asn1.ObjectIdentifier }
		Signers []asn1.RawValue `asn1:"set"`
	}{Version: 1, Content: struct{ Type asn1.ObjectIdentifier }{asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 1}}})
	if err != nil {
		t.Fatal(err)
	}
	p7b, err := asn1.Marshal(struct {
		Type    asn1.ObjectIdentifier
		Content asn1.RawValue
	}{asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 2}, asn1.RawValue{Class: 2, Tag: 0, IsCompound: true, Bytes: signed}})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	data := [][]byte{pk8, p7b}
	files := []string{filepath.Join(dir, "any key name.bin"), filepath.Join(dir, "any certificate name.bin")}
	secrets := [][]byte{pk8, p7b}
	if format == "pem" {
		data = [][]byte{pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pk8}), pem.EncodeToMemory(&pem.Block{Type: "CMS", Bytes: p7b})}
		secrets = append(secrets, data...)
	}
	if format == "zip" {
		var archive bytes.Buffer
		z := zip.NewWriter(&archive)
		for i, name := range []string{"nested/identity.pk8", "nested/certificate.p7b", "extra/member.txt"} {
			w, err := z.Create(name)
			if err != nil {
				t.Fatal(err)
			}
			payload := []byte("ZIP member fixture data must not escape")
			if i < 2 {
				payload = data[i]
			} else {
				secrets = append(secrets, payload)
			}
			if _, err := w.Write(payload); err != nil {
				t.Fatal(err)
			}
		}
		if err := z.Close(); err != nil {
			t.Fatal(err)
		}
		files, data = []string{filepath.Join(dir, "renamed archive.bin")}, [][]byte{archive.Bytes()}
	}
	for i, file := range files {
		if err := os.WriteFile(file, data[i], 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if len(files) == 2 {
		files[0], files[1] = files[1], files[0]
	}
	return files, secrets
}

func assertNextSecrecy(t *testing.T, output string, secrets [][]byte) {
	t.Helper()
	for _, secret := range secrets {
		hash := sha256.Sum256(secret)
		for _, value := range []string{string(secret), base64.StdEncoding.EncodeToString(secret), base64.RawStdEncoding.EncodeToString(secret), hex.EncodeToString(secret), strings.ToUpper(hex.EncodeToString(secret)), hex.EncodeToString(hash[:])} {
			if strings.Contains(output, value) {
				t.Fatal("identity bytes or derived values escaped into output or logs")
			}
		}
	}
}

func TestCLIIdentityStatusMetadataOnly(t *testing.T) {
	svc, d, _ := nextFixture(t)
	for _, status := range []string{"absent", "present", "unknown"} {
		d.sizes["identity.pk8"], d.sizes["certificate.p7b"] = 0, 0
		d.badStatus = status == "unknown"
		if status == "present" {
			d.sizes["identity.pk8"], d.sizes["certificate.p7b"] = 123, 456
		}
		var out, errout bytes.Buffer
		if code := Main([]string{"identity", "status"}, &out, &errout); code != 0 || out.String() != status+"\n" || errout.Len() != 0 {
			t.Fatalf("status %s failed, exit %d", status, code)
		}
	}
	for _, command := range d.commands {
		if strings.Contains(command, "/mfi/") && (!strings.Contains(command, "stat -c '%s'") || strings.Contains(command, "sha256") || strings.Contains(command, "cat ") || strings.Contains(command, "journal")) {
			t.Fatal("status read credential contents or ran a diagnostic")
		}
	}
	if len(d.views) != 0 || svc.InstallLog() != "" {
		t.Fatal("status wrote files or installation logs")
	}
}

func TestCLIIdentityImportAndSecrecy(t *testing.T) {
	for _, format := range []string{"der", "pem", "zip"} {
		t.Run(format, func(t *testing.T) {
			svc, d, _ := nextFixture(t)
			files, secrets := nextIdentityFiles(t, format)
			var out, errout, logs bytes.Buffer
			previous := log.Writer()
			log.SetOutput(&logs)
			t.Cleanup(func() { log.SetOutput(previous) })
			code := Main(append([]string{"identity", "import"}, files...), &out, &errout)
			assertNextSecrecy(t, out.String()+errout.String()+logs.String()+svc.InstallLog()+strings.Join(d.commands, "\n"), secrets)
			if code != 0 || out.String() != "Identity added / 已添加身份\n" || errout.Len() != 0 || !d.restarted || len(d.views) != 2 {
				t.Fatal("identity import did not use the service write/restart path")
			}
			for _, view := range d.views {
				if !bytes.Equal(view, make([]byte, len(view))) {
					t.Fatal("credential buffer was not cleared")
				}
			}
			out.Reset()
			if code = Main([]string{"identity", "status"}, &out, &errout); code != 0 || out.String() != "present\n" {
				t.Fatal("imported identity not observed")
			}
			// Replacement uses the same path, including when metadata already says present.
			out.Reset()
			if code = Main(append([]string{"identity", "import"}, files...), &out, &errout); code != 0 {
				t.Fatal("identity replacement failed")
			}
			d.failWrite = true
			out.Reset()
			if code = Main(append([]string{"identity", "import"}, files...), &out, &errout); code != 1 || !strings.Contains(errout.String(), "[identity-write]") {
				t.Fatal("identity write failure was not typed")
			}
			assertNextSecrecy(t, out.String()+errout.String()+logs.String()+svc.InstallLog(), secrets)
			if strings.Contains(errout.String(), "private transport") {
				t.Fatal("transport diagnostic escaped")
			}
		})
	}
}

func TestCLINextStepsRefuseInvalidTargets(t *testing.T) {
	for _, command := range [][]string{{"identity", "status"}, {"identity", "import"}, {"admin"}} {
		for _, scenario := range []string{"no-device", "several-devices", "relay-missing", "not-root", "not-adb"} {
			t.Run(strings.Join(command, "-")+"/"+scenario, func(t *testing.T) {
				svc, d, bus := nextFixture(t)
				files, secrets := nextIdentityFiles(t, "der")
				switch scenario {
				case "no-device":
					bus.Set()
				case "several-devices":
					bus.Set(usb.DeviceInfo{VendorID: 0x18d1, ProductID: 0xd001, Bus: 1, Address: 1}, usb.DeviceInfo{VendorID: 0x18d1, ProductID: 0xd001, Bus: 1, Address: 2})
				case "relay-missing":
					d.version = ""
				case "not-root":
					d.root = false
				case "not-adb":
					bus.Set(usb.DeviceInfo{VendorID: 0x18d1, ProductID: 0xd00d, Bus: 1, Address: 1})
				}
				args := append([]string{}, command...)
				if len(command) == 2 && command[1] == "import" {
					args = append(args, files...)
				}
				var out, errout bytes.Buffer
				code := Main(args, &out, &errout)
				assertNextSecrecy(t, out.String()+errout.String()+svc.InstallLog(), secrets)
				if code != 1 || out.Len() != 0 || !strings.Contains(errout.String(), "["+scenario+"]") || !strings.Contains(errout.String(), " / ") || len(d.views) != 0 || svc.ForwardStatus().Active {
					t.Fatalf("unsafe target %s was not refused: exit %d, %s", scenario, code, errout.String())
				}
			})
		}
	}
}

func TestCLIIdentityWrongFilesAreTypedAndPrivate(t *testing.T) {
	_, d, _ := nextFixture(t)
	files, secrets := nextIdentityFiles(t, "der")
	wrong := filepath.Join(t.TempDir(), "wrong.bin")
	if err := os.WriteFile(wrong, []byte("not an identity"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		files []string
		code  string
	}{
		{[]string{wrong}, "identity-unrecognized"}, {files[:1], "identity-missing"},
		{[]string{filepath.Join(t.TempDir(), "missing.bin")}, "identity-read"},
		{[]string{files[0], files[0]}, "identity-duplicate"},
	} {
		var out, errout bytes.Buffer
		code := Main(append([]string{"identity", "import"}, tc.files...), &out, &errout)
		assertNextSecrecy(t, out.String()+errout.String(), secrets)
		if code != 1 || !strings.Contains(errout.String(), "["+tc.code+"]") || !strings.Contains(errout.String(), " / ") || len(d.commands) != 0 {
			t.Fatal("invalid identity reached the device or lost its typed bilingual error")
		}
	}
	var out bytes.Buffer
	nextStepsFailure(&out, &stage.Problem{Code: string(secrets[0]), Detail: string(secrets[1])})
	assertNextSecrecy(t, out.String(), secrets)
}

type adminURLWriter struct{ address chan string }

func (w adminURLWriter) Write(data []byte) (int, error) {
	w.address <- strings.TrimSpace(string(data))
	return len(data), nil
}

func TestCLIAdminUSBStaysOpenUntilCancellation(t *testing.T) {
	svc, _, _ := nextFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	previous := nextStepsContext
	nextStepsContext = func() (context.Context, context.CancelFunc) { return ctx, func() {} }
	t.Cleanup(func() { cancel(); nextStepsContext = previous })
	address := make(chan string, 1)
	finished := make(chan int, 1)
	go func() { finished <- Main([]string{"admin"}, adminURLWriter{address}, io.Discard) }()
	var rawURL string
	select {
	case rawURL = <-address:
	case <-time.After(3 * time.Second):
		t.Fatal("admin command did not publish a URL")
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || u.Hostname() != "127.0.0.1" || !svc.ForwardStatus().Active {
		t.Fatal("admin URL is not a service-owned loopback tunnel")
	}
	select {
	case <-finished:
		t.Fatal("admin exited before Ctrl-C")
	default:
	}
	c, err := net.DialTimeout("tcp", u.Host, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err = c.Write([]byte("admin-fixture")); err != nil {
		t.Fatal(err)
	}
	b := make([]byte, len("admin-fixture"))
	if _, err = io.ReadFull(c, b); err != nil || string(b) != "admin-fixture" {
		t.Fatal("admin tunnel did not forward")
	}
	cancel()
	select {
	case code := <-finished:
		if code != 0 || svc.ForwardStatus().Active {
			t.Fatal("Ctrl-C did not stop the tunnel")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("admin did not stop on Ctrl-C")
	}
	if _, err = c.Read(b); err == nil {
		t.Fatal("active stream survived Ctrl-C")
	}
	if c, err := net.DialTimeout("tcp", u.Host, time.Second); err == nil {
		c.Close()
		t.Fatal("listener survived Ctrl-C")
	}
}

func TestCLINextStepsHelpAndUsage(t *testing.T) {
	for _, args := range [][]string{{"help"}, {"identity", "--help"}, {"identity", "status", "--help"}, {"admin", "--help"}} {
		var out, errout bytes.Buffer
		if code := Main(args, &out, &errout); code != 0 || !strings.Contains(out.String(), "identity import") || !strings.Contains(out.String(), "身份") || !strings.Contains(out.String(), "admin [--ssh") {
			t.Fatal("bilingual next steps help missing")
		}
	}
	for _, args := range [][]string{{"identity"}, {"identity", "wrong"}, {"identity", "status", "extra"}, {"identity", "import"}, {"admin", "extra"}} {
		var out, errout bytes.Buffer
		if code := Main(args, &out, &errout); code != 2 {
			t.Fatal("invalid syntax accepted")
		}
	}
}

func TestCLINextStepsSSH(t *testing.T) {
	svc, _, _ := nextFixture(t)
	const password = "next-steps-fixture-password"
	t.Setenv("OXP_FLASHER_SSH_PASSWORD", password)
	board := testssh.NewN1(t, password)
	original := board.Server.Handle
	var mu sync.Mutex
	sizes := map[string]int{}
	relay, badStatus, restarted := true, false, false
	board.Server.Handle = func(raw string, in io.Reader, out, errout io.Writer) int {
		command := testssh.Unwrap(raw)
		mu.Lock()
		defer mu.Unlock()
		switch {
		case strings.HasPrefix(command, "dpkg-query ") && strings.Contains(command, "'openxplay-relay'") && !relay:
			return 1
		case strings.Contains(command, "stat -c '%s'"):
			if badStatus {
				return 1
			}
			fmt.Fprintf(out, "%d\n%d\n", sizes["identity.pk8"], sizes["certificate.p7b"])
			return 0
		case strings.Contains(command, "target='/var/lib/openxplay/mfi/.import-"):
			data, err := io.ReadAll(in)
			if err != nil || !strings.Contains(command, "chmod 600") {
				return 1
			}
			name := "identity.pk8"
			if strings.Contains(command, "/certificate.p7b'") {
				name = "certificate.p7b"
			}
			sizes[name] = len(data)
			clear(data)
			return 0
		case strings.Contains(command, "/var/lib/openxplay/mfi"):
			restarted = restarted || strings.Contains(command, "systemctl restart openxplay-relay.service")
			return 0
		default:
			return original(raw, in, out, errout)
		}
	}
	previous := commandService
	commandService = func(hardware bool, events app.Events) (*app.Service, func(), error) {
		if hardware {
			t.Fatal("SSH requested hardware access")
		}
		return previous(hardware, events)
	}
	host, port := board.Server.HostPort()
	flags := []string{"--ssh", fmt.Sprintf("root@%s:%d", host, port), "--accept-host-key", board.Server.Fingerprint()}
	files, secrets := nextIdentityFiles(t, "zip")
	secrets = append(secrets, []byte(password))
	var logs bytes.Buffer
	oldLog := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(oldLog) })
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"identity", "status"}, "absent\n"},
		{append([]string{"identity", "import"}, files...), "Identity added / 已添加身份\n"},
		{[]string{"identity", "status"}, "present\n"},
		{[]string{"admin"}, "https://127.0.0.1:6975/\n"},
	} {
		var out, errout bytes.Buffer
		code := Main(append(tc.args, flags...), &out, &errout)
		assertNextSecrecy(t, out.String()+errout.String()+logs.String()+svc.InstallLog()+strings.Join(board.Commands, "\n"), secrets)
		if code != 0 || out.String() != tc.want || svc.ForwardStatus().Active {
			t.Fatalf("SSH command failed: %s, exit %d, %s", tc.args[0], code, errout.String())
		}
	}
	mu.Lock()
	didRestart := restarted
	badStatus = true
	mu.Unlock()
	if !didRestart {
		t.Fatal("SSH import did not restart relay")
	}
	var out, errout bytes.Buffer
	if code := Main(append([]string{"identity", "status"}, flags...), &out, &errout); code != 0 || out.String() != "unknown\n" {
		t.Fatal("SSH unreadable metadata was not unknown")
	}
	mu.Lock()
	relay = false
	mu.Unlock()
	for _, args := range [][]string{{"identity", "status"}, append([]string{"identity", "import"}, files...), {"admin"}} {
		out.Reset()
		errout.Reset()
		if code := Main(append(args, flags...), &out, &errout); code != 1 || !strings.Contains(errout.String(), "[relay-missing]") {
			t.Fatal("SSH missing relay accepted")
		}
		assertNextSecrecy(t, out.String()+errout.String()+logs.String()+svc.InstallLog(), secrets)
	}
}
