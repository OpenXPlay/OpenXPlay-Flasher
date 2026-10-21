// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package app

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"strings"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/adb"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/base"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/device"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/profile"
	sshdev "github.com/OpenXPlay/OpenXPlay-Flasher/internal/ssh"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/stage"
)

// SetSSHDialer wires the TCP boundary before use, like SetADB. Production uses net.Dialer.
func (s *Service) SetSSHDialer(d sshdev.Dialer) { s.mu.Lock(); s.sshDialer = d; s.mu.Unlock() }

type sshSession struct {
	connector *sshdev.Connector
	candidate device.Candidate
	serial    string
}

// ConnectionState contains form metadata only. Authentication material never enters this value.
type ConnectionState struct {
	Profile string            `json:"profile"`
	Device  *device.Candidate `json:"device,omitempty"`
}

func (s *Service) Connection() ConnectionState {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := ConnectionState{Profile: s.selectedProfile}
	if s.sshSession != nil {
		c := s.sshSession.candidate
		out.Device = &c
	}
	return out
}

// SelectProfile is the explicit single-device choice. It cannot change underneath a job.
func (s *Service) SelectProfile(id string) error {
	if !s.configMu.TryLock() {
		return ErrDeviceBusy
	}
	defer s.configMu.Unlock()
	var found bool
	for _, p := range s.profiles {
		found = found || p.ID == id
	}
	if !found {
		return ErrUnconfirmed
	}
	s.ForwardStop()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.selectedProfile == id {
		return nil
	}
	s.selectedProfile = id
	s.sshSession = nil
	s.resetTargetLocked()
	return nil
}
func (s *Service) resetTargetLocked() {
	s.installTarget = nil
	s.installed = nil
	s.baseIdentity = nil
	s.unfinished = nil
	s.installProblem = nil
	s.installJob = nil
	s.installStale = false
	s.nextSteps = NextSteps{Identity: "unknown"}
}
func (s *Service) selectedSnapshot(snap device.Snapshot) device.Snapshot {
	s.mu.Lock()
	id := s.selectedProfile
	session := s.sshSession
	s.mu.Unlock()
	if session != nil && id != "" && len(session.candidate.Profiles) == 1 && session.candidate.Profiles[0] == id {
		return device.Snapshot{Candidates: []device.Candidate{session.candidate}}
	}
	if id == "" {
		return snap
	}
	out := snap
	out.Candidates = nil
	for _, c := range snap.Candidates {
		for _, p := range c.Profiles {
			if p == id {
				out.Candidates = append(out.Candidates, c)
				break
			}
		}
	}
	return out
}
func (s *Service) addSSH(snap device.Snapshot) device.Snapshot {
	s.mu.Lock()
	session := s.sshSession
	s.mu.Unlock()
	if session == nil {
		return snap
	}
	out := snap
	out.Candidates = nil
	for _, c := range snap.Candidates {
		if c.Mode == device.ModeSSH && (c.Host == session.candidate.Host && c.Host != "" || c.Serialno == session.serial || session.candidate.Connection == "usb" && c.USB.Location() == session.candidate.USB.Location() || c.USB.Serial != "" && strings.EqualFold(c.USB.Serial, session.serial)) {
			continue
		}
		out.Candidates = append(out.Candidates, c)
	}
	out.Candidates = append(out.Candidates, session.candidate)
	return out
}
func (s *Service) SSHKnownHosts() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dataDir == "" {
		return ""
	}
	return filepath.Join(s.dataDir, "known_hosts")
}
func (s *Service) SSHForget(host string, port int) error {
	if !s.configMu.TryLock() {
		return ErrDeviceBusy
	}
	defer s.configMu.Unlock()
	// Validate addresses through the same parser used at connect time, without credentials.
	address, err := sshdev.HostPort(host, port)
	if err != nil {
		return err
	}
	if err = sshdev.ForgetHost(s.SSHKnownHosts(), address); err != nil {
		return err
	}
	s.mu.Lock()
	if s.sshSession != nil && s.sshSession.connector.Address() == address {
		s.sshSession = nil
		s.resetTargetLocked()
	}
	s.mu.Unlock()
	s.emitInstall()
	return nil
}
func (s *Service) SSHDisconnect() error {
	if !s.configMu.TryLock() {
		return ErrDeviceBusy
	}
	defer s.configMu.Unlock()
	s.mu.Lock()
	s.sshSession = nil
	s.resetTargetLocked()
	s.mu.Unlock()
	s.emitInstall()
	return nil
}

// SSHConnect authenticates and confirms the model, OS, architecture and stable board MAC
// before publishing a connected device. USB descriptors alone never establish board identity.
func (s *Service) SSHConnect(ctx context.Context, mode string, o sshdev.Options) (InstallState, error) {
	if !s.configMu.TryLock() {
		return s.InstallSnapshot(), ErrDeviceBusy
	}
	defer s.configMu.Unlock()
	var p profile.Profile
	for _, v := range s.profiles {
		if v.Transport == profile.ModeSSH {
			p = v
			break
		}
	}
	if p.ID == "" {
		return s.InstallSnapshot(), ErrUnconfirmed
	}
	candidate := device.Candidate{Mode: device.ModeSSH, Profiles: []string{p.ID}, Connection: mode}
	switch mode {
	case "usb":
		var count int
		for _, c := range s.detector.Scan(ctx).Candidates {
			if c.Mode == device.ModeSSH {
				for _, id := range c.Profiles {
					if id == p.ID {
						candidate.USB = c.USB
						count++
					}
				}
			}
		}
		if count != 1 {
			return s.InstallSnapshot(), ErrNoDevice
		}
		for _, m := range p.Connections {
			if m.ID == mode {
				o.Host, o.Port = m.Host, m.Port
			}
		}
	case "network":
	default:
		return s.InstallSnapshot(), errors.New("Choose USB or network / 请选择 USB 或网络连接")
	}
	s.mu.Lock()
	o.Dialer = s.sshDialer
	s.mu.Unlock()
	o.KnownHosts = s.SSHKnownHosts()
	connector, err := sshdev.NewConnector(o)
	if err != nil {
		return s.InstallSnapshot(), err
	}
	d, err := connector.Connect(ctx)
	if err != nil {
		return s.InstallSnapshot(), err
	}
	defer d.Close()
	facts, err := stage.Inspect(ctx, d.Shell, p, nil)
	if err == nil {
		err = confirmProfile(p, facts)
	}
	var serial string
	if err == nil {
		serial, err = sshSerial(ctx, d)
	}
	if err == nil && candidate.USB.Serial != "" && !strings.EqualFold(candidate.USB.Serial, serial) {
		err = &stage.Problem{Code: "serial-mismatch"}
	}
	if err != nil {
		return s.InstallSnapshot(), err
	}
	host, _, _ := net.SplitHostPort(connector.Address())
	candidate.Host = host
	candidate.Port = o.Port
	if candidate.Port == 0 {
		candidate.Port = 22
	}
	candidate.Serialno = serial
	candidate.Product = facts.DeviceTreeModel
	s.mu.Lock()
	s.resetTargetLocked()
	s.selectedProfile = p.ID
	s.sshSession = &sshSession{connector, candidate, serial}
	s.installTarget = &InstallTarget{Device: candidate, Profile: p.ID}
	s.installed = &facts
	s.baseIdentity = &base.Identity{Mode: profile.ModeSSH, Serial: serial, Location: connector.Address()}
	s.nextSteps.AdminURL = relayURL(host)
	s.mu.Unlock()
	if facts.Layers["relay"] != "" {
		s.observeIdentity(ctx, d, serial)
	}
	return s.emitInstall(), nil
}
func relayURL(host string) string { return "https://" + net.JoinHostPort(host, "6975") + "/" }
func confirmProfile(p profile.Profile, f stage.Facts) error {
	c := p.Confirmation
	if c == nil || f.DeviceTreeModel != c.DeviceTreeModel || f.DebianVersion != c.DebianVersion || f.DPKGArchitecture != c.Architecture {
		return &stage.Problem{Code: "ssh-model", Detail: f.DeviceTreeModel + " · Debian " + f.DebianVersion + " · " + f.DPKGArchitecture}
	}
	if !f.Root {
		return &stage.Problem{Code: "not-root"}
	}
	return nil
}
func sshSerial(ctx context.Context, d adb.Device) (string, error) {
	r, err := d.Shell(ctx, "cat /sys/class/net/eth0/address")
	if err != nil {
		return "", err
	}
	mac, err := net.ParseMAC(strings.TrimSpace(string(r.Stdout)))
	if err != nil || r.ExitCode != 0 || len(mac) != 6 {
		return "", &stage.Problem{Code: "bad-serial"}
	}
	return strings.ReplaceAll(mac.String(), ":", ""), nil
}
func (s *Service) boardSerial(ctx context.Context, d adb.Device) (string, error) {
	s.mu.Lock()
	isSSH := s.sshSession != nil
	s.mu.Unlock()
	if isSSH {
		return sshSerial(ctx, d)
	}
	return base.LinuxSerial(ctx, d)
}

// openDevice is the sole normal transport selection point for layer/configuration operations.
func (s *Service) openDevice(ctx context.Context, c device.Candidate) (adb.Device, error) {
	s.mu.Lock()
	session, connector := s.sshSession, s.adb
	s.mu.Unlock()
	if c.Mode != device.ModeSSH {
		if connector == nil {
			return nil, ErrNoADB
		}
		return connector.Connect(ctx, c.USB)
	}
	if session == nil {
		return nil, &stage.Problem{Code: "ssh-login"}
	}
	d, err := session.connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	serial, err := sshSerial(ctx, d)
	if err == nil && serial != session.serial {
		err = &stage.Problem{Code: "serial-mismatch"}
	}
	if err == nil {
		for _, p := range s.profiles {
			if p.ID == c.Profiles[0] {
				var f stage.Facts
				f, err = stage.Inspect(ctx, d.Shell, p, nil)
				if err == nil {
					err = confirmProfile(p, f)
				}
				break
			}
		}
	}
	if err != nil {
		d.Close()
		return nil, err
	}
	return d, nil
}
