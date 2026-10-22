// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package testssh

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// EnableConfig runs configuration scripts against a temporary filesystem only. Board facts
// still come from N1's scripted answers. The owner shim checks root:root without changing host ownership.
func (n *N1) EnableConfig(t testing.TB) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake Linux configuration scripts need a POSIX shell")
	}
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	for _, dir := range []string{bin, root + "/var/lib/openxplay", root + "/etc/openxplay"} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	shims := map[string]string{
		"chown": "#!/bin/sh\n[ \"$1\" = root:root ]\n",
		"sync":  "#!/bin/sh\nexit 0\n",
		"stat": `#!/bin/sh
if /usr/bin/stat -c %a / >/dev/null 2>&1; then exec /usr/bin/stat "$@"; fi
shift 2; [ "$1" = -- ] && shift
exec /usr/bin/stat -f '%Lp %Su:%Sg %z' "$1"`,
	}
	for name, content := range shims {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(content), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{"/var/lib/openxplay/settings.json", "/etc/openxplay/host.json"} {
		if err := os.WriteFile(root+path, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	n.config = func(command string, in io.Reader, out, stderr io.Writer) int {
		// Every device path is relocated before running a script. Returned canonical paths
		// already point inside root and are safe to use on subsequent editor operations.
		command = strings.NewReplacer(root+"/var/lib/", "@FAKE_VAR@", root+"/etc/openxplay/", "@FAKE_ETC@").Replace(command)
		command = strings.NewReplacer("/var/lib/", root+"/var/lib/", "/etc/openxplay/", root+"/etc/openxplay/").Replace(command)
		command = strings.NewReplacer("@FAKE_VAR@", root+"/var/lib/", "@FAKE_ETC@", root+"/etc/openxplay/").Replace(command)
		// Pull uses a Linux /proc descriptor; emulate only this bounded transfer on macOS.
		if strings.Contains(command, "exec 3<") {
			for _, line := range strings.Split(command, "\n") {
				if strings.HasPrefix(line, "exec 3< ") {
					name := Unwrap("sh -c " + strings.TrimPrefix(line, "exec 3< "))
					data, err := os.ReadFile(name)
					if err != nil {
						return 1
					}
					_, _ = out.Write(data)
					return 0
				}
			}
			return 1
		}
		child := exec.Command("sh", "-c", command)
		child.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"))
		child.Stdin, child.Stdout, child.Stderr = in, out, stderr
		if err := child.Run(); err != nil {
			fmt.Fprint(stderr, "fake configuration script failed")
			return 1
		}
		return 0
	}
	return root
}
