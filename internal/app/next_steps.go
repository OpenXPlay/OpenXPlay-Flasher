// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/adb"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/device"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/identity"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/stage"
)

type ForwardState struct {
	Active  bool   `json:"active"`
	Address string `json:"address,omitempty"`
	Serial  string `json:"serial,omitempty"`
}
type NextSteps struct {
	AdminURL string       `json:"adminUrl,omitempty"`
	Identity string       `json:"identity"` // unknown, absent, present; never credential data
	Serial   string       `json:"serial,omitempty"`
	Forward  ForwardState `json:"forward"`
}
type adminTunnel struct {
	state    ForwardState
	target   device.Candidate
	cancel   context.CancelFunc
	listener net.Listener
	done     chan struct{}
}

// Identity status is metadata only. Do not pull files, calculate hashes, or run a journal
// command here: identities and admin passwords must never enter diagnostic output.
const identitySizes = `set -eu
for name in identity.pk8 certificate.p7b; do
 p=/var/lib/openxplay/mfi/$name
 if [ -f "$p" ] && [ ! -L "$p" ]; then stat -c '%s' "$p"; else printf '0\n'; fi
done`

func identitySize(ctx context.Context, d adb.Device) (int64, int64, error) {
	r, err := d.Shell(ctx, identitySizes)
	if err != nil || r.ExitCode != 0 {
		return 0, 0, &stage.Problem{Code: "identity-status"}
	}
	lines := strings.Fields(strings.ReplaceAll(string(r.Stdout), "\r\n", "\n"))
	if len(lines) != 2 {
		return 0, 0, &stage.Problem{Code: "identity-status"}
	}
	a, ea := strconv.ParseInt(lines[0], 10, 64)
	b, eb := strconv.ParseInt(lines[1], 10, 64)
	if ea != nil || eb != nil || a < 0 || b < 0 {
		return 0, 0, &stage.Problem{Code: "identity-status"}
	}
	return a, b, nil
}
func (s *Service) observeIdentity(ctx context.Context, d adb.Device, serial string) {
	a, b, err := identitySize(ctx, d)
	status := "unknown"
	if err == nil {
		status = "absent"
		if a > 0 && b > 0 {
			status = "present"
		}
	}
	s.mu.Lock()
	s.nextSteps.Identity, s.nextSteps.Serial = status, serial
	s.mu.Unlock()
}

// nextDevice performs fresh one-device, model, serial and installed-relay checks. Its caller
// holds configMu. No credential operation can be redirected by a stale Done card.
func (s *Service) nextDevice(ctx context.Context) (adb.Device, string, error) {
	target, p, err := s.installDevice(ctx)
	if err != nil {
		return nil, "", err
	}
	s.mu.Lock()
	expected := s.baseIdentity
	s.mu.Unlock()
	d, err := s.openDevice(ctx, target.Device)
	if err != nil {
		return nil, "", err
	}
	serial, err := s.boardSerial(ctx, d)
	if err == nil && expected != nil && serial != expected.Serial {
		err = &stage.Problem{Code: "serial-mismatch"}
	}
	if err == nil {
		var facts stage.Facts
		facts, err = stage.Inspect(ctx, d.Shell, p, nil)
		if err == nil && !facts.Root {
			err = &stage.Problem{Code: "not-root"}
		}
		if err == nil && facts.Layers["relay"] == "" {
			err = &stage.Problem{Code: "relay-missing"}
		}
	}
	if err != nil {
		d.Close()
		return nil, "", err
	}
	return d, serial, nil
}

// IdentityStatus checks the current confirmed Linux relay, then observes file metadata only.
// An unreadable observation is unknown; a missing device or relay is a typed error.
func (s *Service) IdentityStatus(ctx context.Context) (NextSteps, error) {
	if !s.configMu.TryLock() {
		return NextSteps{Identity: "unknown"}, ErrDeviceBusy
	}
	defer s.configMu.Unlock()
	d, serial, err := s.nextDevice(ctx)
	if err != nil {
		return NextSteps{Identity: "unknown"}, err
	}
	defer d.Close()
	s.observeIdentity(ctx, d, serial)
	return s.emitInstall().NextSteps, nil
}

func (s *Service) IdentityImport(ctx context.Context, paths []string) (InstallState, error) {
	// Read and parse every input before connecting, even when one file is missing.
	pair, err := identity.Read(paths)
	if err != nil {
		return s.InstallSnapshot(), err
	}
	defer clear(pair.Key)
	defer clear(pair.Certificate)
	if !s.configMu.TryLock() {
		return s.InstallSnapshot(), ErrDeviceBusy
	}
	defer s.configMu.Unlock()
	d, serial, err := s.nextDevice(ctx)
	if err != nil {
		return s.InstallSnapshot(), err
	}
	defer d.Close()
	run := func(command string) error {
		r, err := d.Shell(ctx, command)
		if err != nil || r.ExitCode != 0 {
			return &stage.Problem{Code: "identity-write"}
		}
		return nil
	}
	var token [16]byte
	if _, err = rand.Read(token[:]); err != nil {
		return s.InstallSnapshot(), &stage.Problem{Code: "identity-write"}
	}
	tmp := "/var/lib/openxplay/mfi/.import-" + hex.EncodeToString(token[:])
	// Refuse symlinks before making a root-only directory; adbd may use umask 0 and
	// ignore sync's requested modes, so the directory protects even the initial uploads.
	err = run(`set -eu; umask 077
for p in /var/lib/openxplay /var/lib/openxplay/mfi; do [ ! -L "$p" ]; done
mkdir -p /var/lib/openxplay/mfi
chown 0:0 /var/lib/openxplay/mfi
chmod 700 /var/lib/openxplay/mfi
mkdir -m 700 ` + tmp + `
chown 0:0 ` + tmp + `
chmod 700 ` + tmp)
	if err != nil {
		return s.InstallSnapshot(), err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_, _ = d.Shell(cleanup, "rm -rf "+tmp)
	}()
	for _, f := range []struct {
		name string
		data []byte
	}{{"identity.pk8", pair.Key}, {"certificate.p7b", pair.Certificate}} {
		if err = d.Push(ctx, tmp+"/"+f.name, f.data, 0o600); err != nil {
			return s.InstallSnapshot(), &stage.Problem{Code: "identity-write"}
		}
	}
	err = run(`set -eu
chown 0:0 ` + tmp + `/identity.pk8 ` + tmp + `/certificate.p7b
chmod 600 ` + tmp + `/identity.pk8 ` + tmp + `/certificate.p7b
mv -f ` + tmp + `/identity.pk8 /var/lib/openxplay/mfi/identity.pk8
mv -f ` + tmp + `/certificate.p7b /var/lib/openxplay/mfi/certificate.p7b
systemctl restart openxplay-relay.service`)
	if err == nil {
		a, b, checkErr := identitySize(ctx, d)
		if checkErr != nil || a != int64(len(pair.Key)) || b != int64(len(pair.Certificate)) {
			err = &stage.Problem{Code: "identity-status"}
		}
	}
	s.mu.Lock()
	s.nextSteps.Identity, s.nextSteps.Serial = "unknown", serial
	if err == nil {
		s.nextSteps.Identity = "present"
	}
	s.mu.Unlock()
	return s.emitInstall(), err
}

func (s *Service) ForwardStatus() ForwardState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.nextSteps.Forward
}

// ForwardStart binds only loopback and delegates each accepted connection to tcp:6975 on
// the selected adb transport. Its lifetime is the app/device lifetime, never the HTTP request.
func (s *Service) ForwardStart(ctx context.Context) (ForwardState, error) {
	s.adminMu.Lock()
	defer s.adminMu.Unlock()
	s.mu.Lock()
	stopping := s.stopping
	s.mu.Unlock()
	if stopping {
		return ForwardState{}, &stage.Problem{Code: "forward-unavailable"}
	}
	if !s.configMu.TryLock() {
		return s.ForwardStatus(), ErrDeviceBusy
	}
	defer s.configMu.Unlock()
	d, serial, err := s.nextDevice(ctx)
	if err != nil {
		return s.ForwardStatus(), err
	}
	s.mu.Lock()
	old, life := s.admin, s.lifeCtx
	s.mu.Unlock()
	if old != nil && old.state.Serial == serial {
		d.Close()
		return old.state, nil
	}
	if old != nil {
		s.stopForward()
	}
	if !adb.SupportsTCP(d) {
		d.Close()
		return ForwardState{}, &stage.Problem{Code: "forward-unavailable"}
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		d.Close()
		return ForwardState{}, &stage.Problem{Code: "forward-unavailable"}
	}
	if life == nil {
		life = context.Background()
	}
	life, cancel := context.WithCancel(life)
	target, _, err := s.installDevice(ctx)
	if err != nil {
		cancel()
		ln.Close()
		d.Close()
		return ForwardState{}, err
	}
	state := ForwardState{Active: true, Address: fmt.Sprintf("https://127.0.0.1:%d/", ln.Addr().(*net.TCPAddr).Port), Serial: serial}
	tunnel := &adminTunnel{state: state, target: target.Device, cancel: cancel, listener: ln, done: make(chan struct{})}
	s.mu.Lock()
	s.admin = tunnel
	s.nextSteps.Forward = state
	s.mu.Unlock()
	s.background.Add(1)
	go func() {
		defer s.background.Done()
		defer close(tunnel.done)
		defer func() {
			s.mu.Lock()
			changed := s.admin == tunnel
			if changed {
				s.admin = nil
				s.nextSteps.Forward = ForwardState{}
			}
			s.mu.Unlock()
			if changed {
				s.emitInstall()
			}
		}()
		stop := context.AfterFunc(life, func() { ln.Close() })
		defer stop()
		var connections sync.WaitGroup
		for {
			c, err := ln.Accept()
			if err != nil {
				break
			}
			connections.Add(1)
			go func() {
				defer connections.Done()
				defer c.Close()
				closeLocal := context.AfterFunc(life, func() { c.Close() })
				defer closeLocal()
				// A server transport id can be reused after a reconnect. Bind each new
				// stream to the board serial before forwarding any browser traffic.
				check, stopCheck := context.WithTimeout(life, 5*time.Second)
				actual, err := s.boardSerial(check, d)
				stopCheck()
				if err != nil || actual != serial {
					cancel()
					return
				}
				remote, err := d.(adb.TCPDevice).OpenTCP(life, 6975)
				if err != nil {
					return
				}
				defer remote.Close()
				done := make(chan struct{})
				go func() { _, _ = io.Copy(remote, c); remote.Close(); close(done) }()
				_, _ = io.Copy(c, remote)
				c.Close()
				remote.Close()
				<-done
			}()
		}
		cancel()
		connections.Wait()
		d.Close()
	}()
	s.emitInstall()
	return state, nil
}
func (s *Service) stopForward() bool {
	s.mu.Lock()
	tunnel := s.admin
	s.admin = nil
	s.nextSteps.Forward = ForwardState{}
	s.mu.Unlock()
	if tunnel != nil {
		tunnel.cancel()
		tunnel.listener.Close()
		<-tunnel.done
	}
	return tunnel != nil
}
func (s *Service) ForwardStop() ForwardState {
	s.adminMu.Lock()
	changed := s.stopForward()
	s.adminMu.Unlock()
	if changed {
		s.emitInstall()
	}
	return s.ForwardStatus()
}
func (s *Service) checkForward(snap device.Snapshot) {
	s.mu.Lock()
	tunnel := s.admin
	s.mu.Unlock()
	if tunnel == nil {
		return
	}
	for _, c := range snap.Candidates {
		if c.Mode == device.ModeADB && c.USB.Location() == tunnel.target.USB.Location() && c.USB.ID() == tunnel.target.USB.ID() && c.USB.Address == tunnel.target.USB.Address && c.USB.Serial == tunnel.target.USB.Serial {
			return
		}
	}
	s.ForwardStop()
}
