// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package ssh

import (
	"context"
	"errors"
	"io"
	"net"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/testssh"
)

type connectionDialer func(context.Context, string, string) (net.Conn, error)

func (d connectionDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return d(ctx, network, address)
}

func TestTypedConnectionFailures(t *testing.T) {
	for _, tc := range []struct {
		kind string
		err  error
	}{
		{"refused", &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}},
		{"timeout", context.DeadlineExceeded},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			o := Options{Host: "never-dial.invalid", User: "riko", Password: testPassword, KnownHosts: filepath.Join(t.TempDir(), "known_hosts"), Dialer: connectionDialer(func(context.Context, string, string) (net.Conn, error) { return nil, tc.err })}
			_, err := connector(t, o).Connect(t.Context())
			var problem *ConnectionError
			if !errors.As(err, &problem) || problem.Kind != tc.kind || !errors.Is(err, tc.err) || strings.Contains(err.Error(), testPassword) {
				t.Fatalf("unclear or unsafe error: %v", err)
			}
		})
	}
}

func TestTypedMissingSSHHandshake(t *testing.T) {
	for _, silent := range []bool{false, true} {
		t.Run(map[bool]string{false: "closed-before-greeting", true: "silent-peer"}[silent], func(t *testing.T) {
			a, b := net.Pipe()
			t.Cleanup(func() { a.Close(); b.Close() })
			go func() {
				buf := make([]byte, 256)
				_, _ = b.Read(buf) // Drain the client's version, but never send a server greeting.
				if !silent {
					b.Close()
				}
			}()
			o := Options{Host: "fake.invalid", User: "riko", Password: testPassword, KnownHosts: filepath.Join(t.TempDir(), "known_hosts"), DialTimeout: 20 * time.Millisecond, Dialer: connectionDialer(func(context.Context, string, string) (net.Conn, error) { return a, nil })}
			_, err := connector(t, o).Connect(t.Context())
			var problem *ConnectionError
			want := "no-greeting"
			if silent {
				want = "timeout"
			}
			if !errors.As(err, &problem) || problem.Kind != want {
				t.Fatalf("want %s: %v", want, err)
			}
		})
	}
}

func TestTypedAuthenticationFailure(t *testing.T) {
	s := testssh.New(t, testPassword, nil, func(string, io.Reader, io.Writer, io.Writer) int { return 0 })
	o := options(t, s)
	o.AcceptHostKey, o.Password = s.Fingerprint(), "wrong-password"
	_, err := connector(t, o).Connect(t.Context())
	var problem *ConnectionError
	if !errors.As(err, &problem) || problem.Kind != "authentication" || strings.Contains(err.Error(), o.Password) {
		t.Fatalf("authentication: %v", err)
	}
	// The key was stored during the handshake, even though authentication then failed.
	hosts, err := StoredHosts(o.KnownHosts)
	if err != nil || len(hosts) != 1 {
		t.Fatalf("stored hosts: %v %v", hosts, err)
	}
	if err := ForgetHost(o.KnownHosts, connector(t, o).Address()); err != nil {
		t.Fatal(err)
	}
	hosts, err = StoredHosts(o.KnownHosts)
	if err != nil || len(hosts) != 0 {
		t.Fatalf("forgotten hosts: %v %v", hosts, err)
	}
}
