// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package config

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/adb"
)

// localDevice runs the device scripts on this machine, with stand-ins for systemctl, journalctl
// and GNU stat. The stand-in service fails to stay up while the watched file contains BROKEN.
type localDevice struct {
	bin, watched, state string
	shells              []string
}

func newLocalDevice(t *testing.T, watched string) *localDevice {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the device scripts need a POSIX shell")
	}
	dir := t.TempDir()
	d := &localDevice{bin: filepath.Join(dir, "bin"), watched: watched, state: filepath.Join(dir, "state")}
	if err := os.MkdirAll(d.bin, 0o755); err != nil {
		t.Fatal(err)
	}
	shims := map[string]string{
		"systemctl": `#!/bin/sh
case $1 in
restart) if grep -q BROKEN "$OXPF_WATCHED" 2>/dev/null; then echo failed > "$OXPF_STATE"; else echo active > "$OXPF_STATE"; fi; echo restart >> "$OXPF_STATE.log" ;;
is-active) s=$(head -n 1 "$OXPF_STATE" 2>/dev/null || echo inactive); [ "$2" = --quiet ] || echo "$s"; [ "$s" = active ] ;;
show) if [ "$(head -n 1 "$OXPF_STATE")" = active ]; then echo 0; else echo 3; fi ;;
esac`,
		"journalctl": "#!/bin/sh\necho 'relay: settings.json: invalid value'\n",
		// GNU stat -c '%a %U:%G %s' on systems whose stat is BSD's.
		"stat": `#!/bin/sh
if /usr/bin/stat -c %a / >/dev/null 2>&1; then exec /usr/bin/stat "$@"; fi
shift 2; [ "$1" = -- ] && shift
exec /usr/bin/stat -f '%Lp %Su:%Sg %z' "$1"`,
	}
	for name, body := range shims {
		if err := os.WriteFile(filepath.Join(d.bin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

func (d *localDevice) Shell(ctx context.Context, cmd string) (adb.Result, error) {
	d.shells = append(d.shells, cmd)
	c := exec.CommandContext(ctx, "sh", "-c", cmd)
	c.Env = append(os.Environ(), "PATH="+d.bin+":"+os.Getenv("PATH"), "OXPF_WATCHED="+d.watched, "OXPF_STATE="+d.state)
	var out, errb bytes.Buffer
	c.Stdout, c.Stderr = &out, &errb
	err := c.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return adb.Result{Stdout: out.Bytes(), Stderr: errb.Bytes(), ExitCode: exit.ExitCode()}, nil
	}
	return adb.Result{Stdout: out.Bytes(), Stderr: errb.Bytes()}, err
}

func (d *localDevice) Pull(_ context.Context, p string, max int64) ([]byte, error) {
	data, err := os.ReadFile(p)
	if err == nil && int64(len(data)) > max {
		return nil, adb.ErrTooLarge
	}
	return data, err
}

func (d *localDevice) Push(_ context.Context, p string, data []byte, mode fs.FileMode) error {
	return os.WriteFile(p, data, mode)
}

func (d *localDevice) Close() error { return nil }

// fixture is a temporary "device" holding one settings file.
func fixture(t *testing.T, content string) (*Editor, *localDevice, string) {
	t.Helper()
	dir := t.TempDir()
	target := filepath.Join(dir, "settings.json")
	if content != "" {
		if err := os.WriteFile(target, []byte(content), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	dev := newLocalDevice(t, target)
	ed := NewEditor(dev)
	ed.BackupRoot = filepath.Join(dir, "flasher", "backups")
	ed.StagingDir = filepath.Join(dir, "flasher", "staging")
	ed.HealthWait = 2
	ed.Keep = 3
	return ed, dev, target
}

func TestCheckPath(t *testing.T) {
	for _, p := range []string{"/var/lib/openxplay/mfi/identity.pk8", "/var/lib/openxplay/mfi", "/proc/1/environ", "/dev/mmcblk0", "/boot/x", "/var/lib/openxplay/../openxplay/mfi/x"} {
		if _, err := CheckPath(p); !errors.Is(err, ErrProtected) {
			t.Errorf("CheckPath(%q) = %v, want protected", p, err)
		}
	}
	for _, p := range []string{"relative.json", "", "/etc/x\n"} {
		if _, err := CheckPath(p); !errors.Is(err, ErrBadPath) {
			t.Errorf("CheckPath(%q) = %v, want bad path", p, err)
		}
	}
	if p, err := CheckPath("/etc/openxplay//host.json"); err != nil || p != "/etc/openxplay/host.json" {
		t.Errorf("CheckPath clean = %q %v", p, err)
	}
	if _, err := CheckPath("/var/lib/openxplay/mfi-notes"); err != nil {
		t.Errorf("sibling of a protected directory refused: %v", err)
	}
}

func TestValidate(t *testing.T) {
	if Validate("json", `{"a":1}`) != nil || Validate("text", "x=1\n") != nil {
		t.Fatal("valid content refused")
	}
	for _, c := range []struct{ format, content string }{
		{"json", `{"a":`}, {"json", `[1]`}, {"json", `{} {}`}, {"text", "a\x00b"}, {"text", "\xff"}, {"yaml", "a: 1"},
	} {
		if Validate(c.format, c.content) == nil {
			t.Errorf("Validate(%q, %q) accepted", c.format, c.content)
		}
	}
	if !errors.Is(Validate("text", strings.Repeat("a", MaxSize+1)), ErrTooLarge) {
		t.Error("oversized content accepted")
	}
}

func TestReadFollowsSymlinksAndRefusesBinary(t *testing.T) {
	ed, _, target := fixture(t, `{"hotspotChannel":36}`)
	ctx := context.Background()
	f, err := ed.Read(ctx, target)
	if err != nil || !f.Exists || f.Content != `{"hotspotChannel":36}` || f.Mode != "640" || f.Size != 21 {
		t.Fatalf("Read = %+v, %v", f, err)
	}
	link := filepath.Join(filepath.Dir(target), "link.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if f, err := ed.Read(ctx, link); err != nil || f.Content != `{"hotspotChannel":36}` {
		t.Fatalf("Read through a symlink = %+v, %v", f, err)
	}
	if f, err := ed.Read(ctx, target+".missing"); err != nil || f.Exists {
		t.Fatalf("missing file = %+v, %v", f, err)
	}
	bin := filepath.Join(filepath.Dir(target), "blob")
	os.WriteFile(bin, []byte{1, 0, 2}, 0o600)
	if _, err := ed.Read(ctx, bin); !errors.Is(err, ErrBinary) {
		t.Fatalf("binary file: %v", err)
	}
	if _, err := ed.Read(ctx, filepath.Dir(target)); !errors.Is(err, ErrNotRegular) {
		t.Fatalf("directory: %v", err)
	}
}

func TestWriteKeepsModeBacksUpAndRestarts(t *testing.T) {
	ed, _, target := fixture(t, `{"hotspotChannel":36}`)
	ctx := context.Background()
	res, err := ed.Write(ctx, WriteRequest{Path: target, Content: `{"hotspotChannel":6}`, Format: "json", Service: "openxplay-relay.service"})
	if err != nil || !res.Restarted || !res.Healthy || res.RolledBack || res.Backup == "" {
		t.Fatalf("Write = %+v, %v", res, err)
	}
	if data, _ := os.ReadFile(target); string(data) != `{"hotspotChannel":6}` {
		t.Fatalf("content %q", data)
	}
	if st, _ := os.Stat(target); st.Mode().Perm() != 0o640 {
		t.Fatalf("mode %v not kept", st.Mode().Perm())
	}
	backups, err := ed.Backups(ctx, target)
	if err != nil || len(backups) != 1 || backups[0].ID != res.Backup {
		t.Fatalf("Backups = %+v, %v", backups, err)
	}
	resolved, _ := filepath.EvalSymlinks(target) // backups follow the resolved path (/var is a link on macOS)
	if st, err := os.Stat(ed.backupDir(resolved)); err != nil || st.Mode().Perm() != 0o700 {
		t.Fatalf("backup directory: %v %v", st, err)
	}
	if _, err := os.Stat(target + ".oxpf-tmp"); !os.IsNotExist(err) {
		t.Fatal("temporary file left behind")
	}
	if st, err := os.Stat(ed.StagingDir); err != nil || st.Mode().Perm() != 0o700 {
		t.Fatalf("staging directory: %v %v", st, err)
	}
	if left, _ := os.ReadDir(ed.StagingDir); len(left) != 0 {
		t.Fatalf("uploads left in staging: %v", left)
	}
}

func TestWriteRollsBackWhenTheServiceFails(t *testing.T) {
	ed, _, target := fixture(t, `{"hotspotChannel":36}`)
	res, err := ed.Write(context.Background(), WriteRequest{Path: target, Content: `{"output":"BROKEN"}`, Format: "json", Service: "openxplay-relay.service"})
	if err != nil || res.Healthy || !res.RolledBack || !strings.Contains(res.Log, "invalid value") {
		t.Fatalf("Write = %+v, %v", res, err)
	}
	if data, _ := os.ReadFile(target); string(data) != `{"hotspotChannel":36}` {
		t.Fatalf("not rolled back: %q", data)
	}
}

func TestWriteRefusals(t *testing.T) {
	ed, dev, target := fixture(t, `{}`)
	ctx := context.Background()
	if _, err := ed.Write(ctx, WriteRequest{Path: target, Content: `{"a":`, Format: "json"}); err == nil {
		t.Fatal("invalid JSON written")
	}
	if _, err := ed.Write(ctx, WriteRequest{Path: "/var/lib/openxplay/mfi/identity.pk8", Content: "x", Format: "text"}); !errors.Is(err, ErrProtected) {
		t.Fatalf("protected path: %v", err)
	}
	if len(dev.shells) != 0 {
		t.Fatalf("refused writes reached the device: %v", dev.shells)
	}
}

func TestNewFileAndBackupPruning(t *testing.T) {
	ed, _, target := fixture(t, "")
	ctx := context.Background()
	res, err := ed.Write(ctx, WriteRequest{Path: target, Content: "v0", Format: "text"})
	if err != nil || res.Backup != "" || res.Restarted {
		t.Fatalf("new file = %+v, %v", res, err)
	}
	if st, _ := os.Stat(target); st.Mode().Perm() != 0o600 {
		t.Fatalf("new file mode %v", st.Mode().Perm())
	}
	for i := 1; i <= 5; i++ {
		if _, err := ed.Write(ctx, WriteRequest{Path: target, Content: "v" + string(rune('0'+i)), Format: "text"}); err != nil {
			t.Fatal(err)
		}
	}
	backups, _ := ed.Backups(ctx, target)
	if len(backups) != ed.Keep {
		t.Fatalf("%d backups kept, want %d", len(backups), ed.Keep)
	}
	res, err = ed.Restore(ctx, target, backups[len(backups)-1].ID, "")
	if err != nil || res.Backup == "" {
		t.Fatalf("Restore = %+v, %v", res, err)
	}
	if data, _ := os.ReadFile(target); string(data) != "v2" {
		t.Fatalf("restored %q, want the oldest kept version v2", data)
	}
	if _, err := ed.Restore(ctx, target, "../../etc/passwd", ""); err == nil {
		t.Fatal("backup id with a path accepted")
	}
	missing := filepath.Join(filepath.Dir(target), "no-such-folder", "x.json")
	if _, err := ed.Write(ctx, WriteRequest{Path: missing, Content: "{}", Format: "json"}); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("write into a missing folder: %v", err)
	}
}

func TestQuote(t *testing.T) {
	out, err := exec.Command("sh", "-c", "printf %s "+quote(`it's $HOME "x"`)).Output()
	if runtime.GOOS != "windows" && (err != nil || string(out) != `it's $HOME "x"`) {
		t.Fatalf("quote round trip = %q, %v", out, err)
	}
}

// crlfDevice wraps a device whose shell answers through a terminal, as older adbd versions do.
type crlfDevice struct{ *localDevice }

func (d crlfDevice) Shell(ctx context.Context, cmd string) (adb.Result, error) {
	res, err := d.localDevice.Shell(ctx, cmd)
	res.Stdout = bytes.ReplaceAll(res.Stdout, []byte("\n"), []byte("\r\n"))
	return res, err
}

func TestTerminalLineEndings(t *testing.T) {
	ed, dev, target := fixture(t, `{"a":1}`)
	ed.Dev = crlfDevice{dev}
	if f, err := ed.Read(context.Background(), target); err != nil || f.Content != `{"a":1}` || f.Size != 7 {
		t.Fatalf("Read over CRLF = %+v, %v", f, err)
	}
	if res, err := ed.Write(context.Background(), WriteRequest{Path: target, Content: `{"a":2}`, Format: "json", Service: "relay.service"}); err != nil || !res.Healthy || res.Backup == "" {
		t.Fatalf("Write over CRLF = %+v, %v", res, err)
	}
}
