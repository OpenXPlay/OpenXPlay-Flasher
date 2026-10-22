// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package adb

import (
	"bytes"
	"os/exec"
	"testing"
)

func TestParseSentinel(t *testing.T) {
	cases := []struct {
		out    string
		stdout string
		code   int
	}{
		{"out\n\n__OXPF_EXIT__0\n", "out\n", 0},
		{"x\n__OXPF_EXIT__3\n", "x", 3},
		{"\n__OXPF_EXIT__127\n", "", 127},
		// A terminal that still turns \n into \r\n.
		{"a\r\nb\r\n\r\n__OXPF_EXIT__3\r\n", "a\r\nb\r\n", 3},
		// Exact stock UFI003 getprop output, plus failure and no trailing command newline.
		{"msm8916_32_512\r\n\r\n__OXPF_EXIT__0\r\n", "msm8916_32_512\r\n", 0},
		{"getprop: failed\r\n\r\n__OXPF_EXIT__127\r\n", "getprop: failed\r\n", 127},
		{"msm8916_32_512\r\n__OXPF_EXIT__3\r\n", "msm8916_32_512", 3},
		// Only the last status counts.
		{"\n__OXPF_EXIT__1\nmore\n\n__OXPF_EXIT__2\n", "\n__OXPF_EXIT__1\nmore\n", 2},
		{"\n__OXPF_EXIT__4", "", 4},
	}
	for _, tc := range cases {
		r, err := parseSentinel([]byte(tc.out))
		if err != nil || string(r.Stdout) != tc.stdout || r.ExitCode != tc.code || len(r.Stderr) != 0 {
			t.Errorf("parseSentinel(%q) = %q %d %v", tc.out, r.Stdout, r.ExitCode, err)
		}
	}
	for _, bad := range []string{"no status\n", "\n__OXPF_EXIT__\n", "\n__OXPF_EXIT__x1\r\n"} {
		if _, err := parseSentinel([]byte(bad)); err == nil {
			t.Errorf("parseSentinel(%q) succeeded", bad)
		}
	}
}

func TestSentinelCommandWithoutUtilities(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	for _, tc := range []struct {
		cmd    string
		stdout string
		code   int
	}{
		{"echo msm8916_32_512", "msm8916_32_512\n", 0},
		{"echo failed >&2; exit 127", "failed\n", 127},
		{"exit 3", "", 3},
	} {
		// No external utilities, and no printf builtin as on the measured stock shell.
		c := exec.Command(sh, "-c", "printf() { return 127; }; "+sentinelCommand(tc.cmd))
		c.Env = []string{"PATH=" + t.TempDir()}
		out, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("%q: %v (%q)", tc.cmd, err, out)
		}
		r, err := parseSentinel(out)
		if err != nil || string(r.Stdout) != tc.stdout || r.ExitCode != tc.code {
			t.Errorf("%q: %q %d %v", tc.cmd, r.Stdout, r.ExitCode, err)
		}
	}
}

// TestSentinelCommandInShell runs the wrapper through this machine's sh, stdout and stderr merged
// as the legacy shell service merges them.
func TestSentinelCommandInShell(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	cases := []struct {
		cmd    string
		stdout string
		code   int
	}{
		{"printf 'a\\nb'; echo e >&2; exit 3", "a\nbe\n", 3},
		{"true", "", 0},
		{"echo a # a comment", "a\n", 0},
		{"read x; echo \"got:$x\"", "got:\n", 0},
		{"cat <<EOF\nhi\nEOF", "hi\n", 0},
		{"exit 5", "", 5},
		{"echo ')' \"it's\"", ") it's\n", 0},
	}
	for _, tc := range cases {
		var out bytes.Buffer
		c := exec.Command(sh, "-c", sentinelCommand(tc.cmd))
		c.Stdout, c.Stderr = &out, &out
		if err := c.Run(); err != nil {
			t.Fatalf("%q: %v (%q)", tc.cmd, err, out.Bytes())
		}
		r, err := parseSentinel(out.Bytes())
		if err != nil || string(r.Stdout) != tc.stdout || r.ExitCode != tc.code {
			t.Errorf("%q: %q %d %v", tc.cmd, r.Stdout, r.ExitCode, err)
		}
	}
}

func TestRunShellV2(t *testing.T) {
	var in bytes.Buffer
	writeShellPacket(&in, shellStdout, []byte("o1"))
	writeShellPacket(&in, shellStderr, []byte("e"))
	writeShellPacket(&in, 9, []byte("ignored"))
	writeShellPacket(&in, shellStdout, []byte("o2"))
	writeShellPacket(&in, shellExit, []byte{200})
	var sent bytes.Buffer
	r, err := runShellV2(readWriter{bytes.NewReader(in.Bytes()), &sent})
	if err != nil || string(r.Stdout) != "o1o2" || string(r.Stderr) != "e" || r.ExitCode != 200 {
		t.Fatalf("%+v %v", r, err)
	}
	if !bytes.Equal(sent.Bytes(), []byte{shellCloseStdin, 0, 0, 0, 0}) {
		t.Fatalf("sent %x", sent.Bytes())
	}
	var cut bytes.Buffer
	writeShellPacket(&cut, shellStdout, []byte("o"))
	if _, err := runShellV2(readWriter{bytes.NewReader(cut.Bytes()), &bytes.Buffer{}}); err == nil {
		t.Fatal("no exit packet accepted")
	}
}

type readWriter struct {
	r *bytes.Reader
	w *bytes.Buffer
}

func (rw readWriter) Read(p []byte) (int, error)  { return rw.r.Read(p) }
func (rw readWriter) Write(p []byte) (int, error) { return rw.w.Write(p) }
