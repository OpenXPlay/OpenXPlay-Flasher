// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package ssh

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/adb"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/testssh"
	cryptossh "golang.org/x/crypto/ssh"
)

const testPassword = "memory-only-S3cret!"

func options(t *testing.T, s *testssh.Server) Options {
	h, p := s.HostPort()
	return Options{Host: h, Port: p, User: "root", Password: testPassword, KnownHosts: filepath.Join(t.TempDir(), "known_hosts"), DialTimeout: time.Second, CommandTimeout: time.Second, KeepaliveInterval: time.Hour}
}
func connector(t *testing.T, o Options) *Connector {
	t.Helper()
	c, err := NewConnector(o)
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func TestHostKeyLifecycle(t *testing.T) {
	s := testssh.New(t, testPassword, nil, func(cmd string, in io.Reader, out, stderr io.Writer) int { return 0 })
	o := options(t, s)
	c := connector(t, o)
	_, err := c.Connect(context.Background())
	var key *HostKeyError
	if !errors.As(err, &key) || key.Changed || key.Fingerprint != s.Fingerprint() || key.KeyType != "ssh-ed25519" {
		t.Fatalf("unknown: %v", err)
	}
	if _, err = os.Stat(o.KnownHosts); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("first use wrote without consent")
	}
	o.AcceptHostKey = key.Fingerprint
	d, err := connector(t, o).Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	d.Close()
	o.AcceptHostKey = ""
	d, err = connector(t, o).Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	d.Close()
	data, _ := os.ReadFile(o.KnownHosts)
	if strings.Contains(string(data), testPassword) {
		t.Fatal("password persisted")
	}
	info, _ := os.Stat(o.KnownHosts)
	if info.Mode().Perm() != 0o600 {
		t.Fatal("known_hosts mode")
	}
	changed, _ := testssh.NewSigner(t)
	s.ChangeKey(changed)
	o.AcceptHostKey = s.Fingerprint()
	_, err = connector(t, o).Connect(context.Background())
	if !errors.As(err, &key) || !key.Changed {
		t.Fatalf("changed accepted: %v", err)
	}
	if err = ForgetHost(o.KnownHosts, c.Address()); err != nil {
		t.Fatal(err)
	}
	o.AcceptHostKey = ""
	_, err = connector(t, o).Connect(context.Background())
	if !errors.As(err, &key) || key.Changed {
		t.Fatal(err)
	}
	o.AcceptHostKey = s.Fingerprint()
	d, err = connector(t, o).Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	d.Close()
}
func TestPasswordAndEncryptedKey(t *testing.T) {
	signer, key := testssh.NewSigner(t)
	s := testssh.New(t, testPassword, signer.PublicKey(), func(cmd string, in io.Reader, out, stderr io.Writer) int {
		return testssh.Result(out, stderr, "stdout", "stderr", 7)
	})
	o := options(t, s)
	o.AcceptHostKey = s.Fingerprint()
	d, err := connector(t, o).Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	r, err := d.Shell(context.Background(), "hello")
	d.Close()
	if err != nil || string(r.Stdout) != "stdout" || string(r.Stderr) != "stderr" || r.ExitCode != 7 {
		t.Fatalf("%+v %v", r, err)
	}
	o.Password = "wrong"
	if d, err = connector(t, o).Connect(context.Background()); err == nil {
		d.Close()
		t.Fatal("wrong login password accepted")
	}
	block, err := cryptossh.MarshalPrivateKeyWithPassphrase(key, "test", []byte("key-pass"))
	if err != nil {
		t.Fatal(err)
	}
	o.KeyFile = filepath.Join(t.TempDir(), "id_ed25519")
	if err = os.WriteFile(o.KeyFile, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	o.Password = ""
	_, err = NewConnector(o)
	var missing *KeyPassphraseError
	if !errors.As(err, &missing) {
		t.Fatal(err)
	}
	o.Passphrase = "wrong"
	if _, err = NewConnector(o); err == nil {
		t.Fatal("wrong passphrase accepted")
	}
	o.Passphrase = "key-pass"
	d, err = connector(t, o).Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	d.Close()
}
func TestSudoFramingAndErrors(t *testing.T) {
	for _, kind := range []string{"password", "nopass", "incorrect-password", "not-sudoer", "key-only", "key-nopass"} {
		t.Run(kind, func(t *testing.T) {
			signer, key := testssh.NewSigner(t)
			block, _ := cryptossh.MarshalPrivateKey(key, "test")
			keyFile := filepath.Join(t.TempDir(), "key")
			_ = os.WriteFile(keyFile, pem.EncodeToMemory(block), 0o600)
			s := testssh.New(t, testPassword, signer.PublicKey(), func(cmd string, in io.Reader, out, stderr io.Writer) int {
				if strings.Contains(cmd, "sudo -n -p '' -- true") {
					if kind == "nopass" || kind == "key-nopass" {
						return 0
					}
					if kind == "not-sudoer" {
						return testssh.Result(out, stderr, "", "user is not in the sudoers file", 1)
					}
					return testssh.Result(out, stderr, "", "sudo: a password is required", 1)
				}
				reader := bufio.NewReader(in)
				if kind != "nopass" && kind != "key-nopass" {
					line, _ := reader.ReadString('\n')
					if line != testPassword+"\n" {
						t.Errorf("sudo framing")
					}
					if strings.HasSuffix(cmd, "sudo -k -S -p '' -- true") {
						if kind == "incorrect-password" {
							return testssh.Result(out, stderr, "", "sudo: 1 incorrect password attempt", 1)
						}
						return 0
					}
					if !strings.Contains(cmd, "sudo -k -S -p '' -- sh -c") {
						t.Error("sudo must consume password even with timestamp cache")
					}
				}
				if kind == "incorrect-password" {
					return testssh.Result(out, stderr, "", "sudo: 1 incorrect password attempt", 1)
				}
				if strings.Contains(cmd, "mktemp") {
					data, _ := io.ReadAll(reader)
					if string(data) != "file\ninput\x00data" {
						t.Errorf("stdin data after password corrupted: %d bytes", len(data))
					}
					return 0
				}
				if kind == "key-nopass" {
					return testssh.Result(out, stderr, "root", "", 0)
				}
				return testssh.Result(out, stderr, testPassword, "", 0)
			})
			o := options(t, s)
			o.User = "user"
			o.AcceptHostKey = s.Fingerprint()
			if strings.HasPrefix(kind, "key-") {
				o.Password = ""
				o.KeyFile = keyFile
			}
			d, err := connector(t, o).Connect(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			r, err := d.Shell(context.Background(), "id -u")
			if kind == "incorrect-password" || kind == "not-sudoer" || kind == "key-only" {
				var e *SudoError
				if !errors.As(err, &e) {
					t.Fatalf("typed sudo error: %v", err)
				}
				want := kind
				if kind == "key-only" {
					want = "password-required"
				}
				if e.Kind != want {
					t.Fatalf("%s", e.Kind)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(r.Stdout), testPassword) {
				t.Fatal("credential in loggable result")
			}
			if err = d.Push(context.Background(), "/tmp/config", []byte("file\ninput\x00data"), 0o640); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestPushAtomicModesAndPullLimit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("local POSIX script fixture")
	}
	target := filepath.Join(t.TempDir(), "a 'quoted' file")
	_ = os.WriteFile(target, []byte("old"), 0o644)
	s := testssh.New(t, testPassword, nil, func(cmd string, in io.Reader, out, stderr io.Writer) int {
		script := testssh.Unwrap(cmd)
		if strings.Contains(script, "exec 3<") {
			if !strings.Contains(script, "stat -Lc") || !strings.Contains(script, "head -c") {
				t.Error("unbounded pull")
			}
			var limit int64
			for _, line := range strings.Split(script, "\n") {
				if strings.HasPrefix(line, "[ \"$size\" -le ") {
					fmt.Sscanf(line, "[ \"$size\" -le %d", &limit)
				}
			}
			data, _ := os.ReadFile(target)
			if int64(len(data)) > limit {
				return 73
			}
			_, _ = out.Write(data)
			return 0
		}
		if !strings.Contains(script, "umask 077") || !strings.Contains(script, "mktemp") || !strings.Contains(script, "mv -f") {
			t.Error("non-atomic upload")
		}
		command := exec.Command("sh", "-c", script)
		command.Stdin = in
		command.Stdout = out
		command.Stderr = stderr
		if err := command.Run(); err != nil {
			return 1
		}
		return 0
	})
	o := options(t, s)
	o.AcceptHostKey = s.Fingerprint()
	d, err := connector(t, o).Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	data := []byte("new\x00contents\n")
	if err = d.Push(context.Background(), target, data, 0o640); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(target)
	info, _ := os.Stat(target)
	if !bytes.Equal(data, got) || info.Mode().Perm() != 0o640 {
		t.Fatal("replace or mode failed")
	}
	matches, _ := filepath.Glob(target + ".oxpf-*")
	if len(matches) > 0 {
		t.Fatal("temporary file left behind")
	}
	if _, err = d.Pull(context.Background(), target, 2); !errors.Is(err, adb.ErrTooLarge) {
		t.Fatal(err)
	}
	got, err = d.Pull(context.Background(), target, int64(len(data)))
	if err != nil || !bytes.Equal(got, data) {
		t.Fatal(err)
	}
}
func TestCommandTimeout(t *testing.T) {
	s := testssh.New(t, testPassword, nil, func(cmd string, in io.Reader, out, stderr io.Writer) int { time.Sleep(80 * time.Millisecond); return 0 })
	o := options(t, s)
	o.AcceptHostKey = s.Fingerprint()
	o.CommandTimeout = 10 * time.Millisecond
	d, err := connector(t, o).Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	_, err = d.Shell(context.Background(), "slow")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}

func TestCredentialFormatting(t *testing.T) {
	options := Options{Password: testPassword, Passphrase: "private-key-secret"}
	encoded, err := json.Marshal(options)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{string(encoded), fmt.Sprintf("%+v", options), fmt.Sprintf("%#v", options)} {
		if strings.Contains(value, testPassword) || strings.Contains(value, "private-key-secret") {
			t.Fatal("credential in diagnostics")
		}
	}
}
