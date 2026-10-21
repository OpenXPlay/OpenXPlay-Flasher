// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package config

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	sshdev "github.com/OpenXPlay/OpenXPlay-Flasher/internal/ssh"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/testssh"
)

func TestEditorOverSSH(t *testing.T) {
	ed, local, target := fixture(t, `{"original":true}`)
	s := testssh.New(t, "config-memory-secret", nil, func(cmd string, in io.Reader, out, stderr io.Writer) int {
		command := testssh.Unwrap(cmd)
		if strings.Contains(command, "exec 3<") {
			var name string
			for _, line := range strings.Split(command, "\n") {
				if strings.HasPrefix(line, "exec 3< ") {
					name = testssh.Unwrap("sh -c " + strings.TrimPrefix(line, "exec 3< "))
				}
			}
			data, err := os.ReadFile(name)
			if err != nil {
				return 1
			}
			_, _ = out.Write(data)
			return 0
		}
		if strings.Contains(command, "mktemp") {
			child := exec.Command("sh", "-c", command)
			child.Stdin = in
			child.Stdout = out
			child.Stderr = stderr
			if err := child.Run(); err != nil {
				return 1
			}
			return 0
		}
		r, err := local.Shell(context.Background(), command)
		if err != nil {
			return 1
		}
		_, _ = out.Write(r.Stdout)
		_, _ = stderr.Write(r.Stderr)
		return r.ExitCode
	})
	host, port := s.HostPort()
	connector, err := sshdev.NewConnector(sshdev.Options{Host: host, Port: port, User: "root", Password: "config-memory-secret", KnownHosts: filepath.Join(t.TempDir(), "known_hosts"), AcceptHostKey: s.Fingerprint()})
	if err != nil {
		t.Fatal(err)
	}
	d, err := connector.Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	ed.Dev = d
	file, err := ed.Read(context.Background(), target)
	if err != nil || file.Content != `{"original":true}` {
		t.Fatalf("read: %+v %v", file, err)
	}
	result, err := ed.Write(context.Background(), WriteRequest{Path: target, Content: `{"updated":true}`, Format: "json", Service: "relay.service"})
	if err != nil || !result.Healthy || result.Backup == "" {
		t.Fatalf("write: %+v %v", result, err)
	}
	info, _ := os.Stat(target)
	mode, _ := strconv.ParseUint(file.Mode, 8, 32)
	if info.Mode().Perm() != os.FileMode(mode) {
		t.Fatal("configuration mode changed")
	}
	file, err = ed.Read(context.Background(), target)
	if err != nil || file.Content != `{"updated":true}` {
		t.Fatal("write readback", err)
	}
	backups, err := ed.Backups(context.Background(), target)
	if err != nil || len(backups) != 1 {
		t.Fatal("backups", err)
	}
}
