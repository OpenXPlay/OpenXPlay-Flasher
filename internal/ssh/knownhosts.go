// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

// Package ssh implements the shared Device boundary over authenticated SSH. Credentials are
// held only by an in-memory Connector; neither commands nor file contents are logged here.
package ssh

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"

	cryptossh "golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// HostKeyError distinguishes first use from a key change. A changed key can never be accepted
// by the fingerprint flag; the user must explicitly forget the old host first.
type HostKeyError struct {
	Host        string `json:"host"`
	Fingerprint string `json:"fingerprint"`
	Changed     bool   `json:"changed"`
}

func (e *HostKeyError) Permanent() bool { return true }
func (e *HostKeyError) Error() string {
	if e.Changed {
		return fmt.Sprintf("SSH host key changed for %s; connection refused. Verify the box before forgetting this host. / %s 的 SSH 主机密钥已改变，已拒绝连接。请先核实设备，再忘记此主机。 (%s)", e.Host, e.Host, e.Fingerprint)
	}
	return fmt.Sprintf("Unknown SSH host %s / 未信任的 SSH 主机 %s: %s", e.Host, e.Host, e.Fingerprint)
}

var hostsMu sync.Mutex

func hostCallback(file, accept string) cryptossh.HostKeyCallback {
	return func(host string, remote net.Addr, key cryptossh.PublicKey) error {
		hostsMu.Lock()
		defer hostsMu.Unlock()
		var err error
		if _, err = os.Stat(file); errors.Is(err, os.ErrNotExist) {
			err = &knownhosts.KeyError{}
		} else if err == nil {
			var cb cryptossh.HostKeyCallback
			cb, err = knownhosts.New(file)
			if err == nil {
				err = cb(host, remote, key)
			}
		}
		if err == nil {
			return nil
		}
		var ke *knownhosts.KeyError
		if !errors.As(err, &ke) {
			return err
		}
		fp := cryptossh.FingerprintSHA256(key)
		if len(ke.Want) > 0 {
			return &HostKeyError{Host: host, Fingerprint: fp, Changed: true}
		}
		if accept != fp {
			return &HostKeyError{Host: host, Fingerprint: fp}
		}
		if err = os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
			return err
		}
		f, err := os.OpenFile(file, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		defer f.Close()
		if err = f.Chmod(0o600); err != nil {
			return err
		}
		_, err = fmt.Fprintln(f, knownhosts.Line([]string{host}, key))
		if err == nil {
			err = f.Sync()
		}
		return err
	}
}

// ForgetHost removes this app's TOFU record, leaving every other host intact. Our records
// contain one normalized address per line; user-managed SSH files are never edited.
func ForgetHost(file, address string) error {
	hostsMu.Lock()
	defer hostsMu.Unlock()
	data, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	host := knownhosts.Normalize(address)
	var kept []string
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) > 0 && fields[0] == host {
			continue
		}
		if line != "" {
			kept = append(kept, line)
		}
	}
	tmp, err := os.CreateTemp(filepath.Dir(file), ".known-hosts-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.WriteString(strings.Join(kept, "\n") + "\n")
	if err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), file)
	}
	return err
}
