// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

// Package config reads and writes configuration files on a Linux relay device over adb. A write
// keeps a backup on the device, replaces the file atomically with its owner and mode, restarts the
// relay when asked and rolls back if the relay does not stay up. The safety rules live here, not
// in profiles: some paths are never read or written, whatever the caller asks.
package config

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/adb"
)

// MaxSize bounds the files the editor handles; configuration files are far smaller.
const MaxSize = 1 << 20

// Protected paths are never read or written, also when a symlink leads there: the accessory
// identity, kernel and device interfaces, and the boot partition.
var protected = []string{"/var/lib/openxplay/mfi", "/proc", "/sys", "/dev", "/boot"}

var (
	ErrProtected  = errors.New("this file is protected and cannot be opened")
	ErrNotRegular = errors.New("not a regular file")
	ErrTooLarge   = errors.New("the file is larger than 1 MiB")
	ErrBinary     = errors.New("not a text file")
	ErrBadPath    = errors.New("the path must be absolute, for example /etc/openxplay/host.json")
)

// InvalidError is content that fails its format check; the message says where.
type InvalidError struct{ Msg string }

func (e *InvalidError) Error() string { return e.Msg }

// File is a configuration file as read from the device.
type File struct {
	Path    string `json:"path"`
	Exists  bool   `json:"exists"`
	Content string `json:"content"`
	Mode    string `json:"mode,omitempty"`
	Owner   string `json:"owner,omitempty"`
	Size    int64  `json:"size"`
}

// Backup is an earlier version of a file kept on the device.
type Backup struct {
	ID   string    `json:"id"`
	Time time.Time `json:"time"`
}

// WriteRequest is one file write.
type WriteRequest struct {
	Path    string
	Content string
	// Format is "json" or "text"; json content must parse.
	Format string
	// Service is the systemd unit to restart after the write, or "" for none.
	Service string
}

// WriteResult reports what a write did.
type WriteResult struct {
	Backup     string `json:"backup,omitempty"`
	Restarted  bool   `json:"restarted"`
	Healthy    bool   `json:"healthy"`
	RolledBack bool   `json:"rolledBack"`
	// Log is the end of the service's journal when it did not stay up.
	Log string `json:"log,omitempty"`
}

// Editor works on one device.
type Editor struct {
	Dev adb.Device
	// BackupRoot holds one directory of backups per file; root-only on the device.
	BackupRoot string
	// StagingDir receives uploads before they replace a file. It is root-only: this adbd ignores
	// the mode sent with an upload, so the file itself is world-readable until it is chmodded.
	StagingDir string
	// HealthWait is how long a restarted service must stay up, in seconds.
	HealthWait int
	// Keep is the number of backups kept per file.
	Keep int
}

// NewEditor returns an editor with the device defaults.
func NewEditor(dev adb.Device) *Editor {
	return &Editor{Dev: dev, BackupRoot: "/var/lib/openxplay-flasher/backups", StagingDir: "/var/lib/openxplay-flasher/staging", HealthWait: 10, Keep: 10}
}

// CheckPath cleans an absolute path and refuses protected ones.
func CheckPath(p string) (string, error) {
	if !path.IsAbs(p) || strings.ContainsAny(p, "\x00\n\r") {
		return "", ErrBadPath
	}
	p = path.Clean(p)
	for _, dir := range protected {
		if p == dir || strings.HasPrefix(p, dir+"/") {
			return "", ErrProtected
		}
	}
	return p, nil
}

// Validate checks content before it is written.
func Validate(format, content string) error {
	if len(content) > MaxSize {
		return ErrTooLarge
	}
	if strings.ContainsRune(content, 0) || !utf8.ValidString(content) {
		return ErrBinary
	}
	switch format {
	case "json":
		var v any
		dec := json.NewDecoder(strings.NewReader(content))
		if err := dec.Decode(&v); err != nil {
			return &InvalidError{"invalid JSON: " + err.Error()}
		}
		if dec.More() {
			return &InvalidError{"invalid JSON: more than one value"}
		}
		if _, ok := v.(map[string]any); !ok {
			return &InvalidError{"invalid JSON: the file must hold one object { ... }"}
		}
	case "text", "":
	default:
		return &InvalidError{fmt.Sprintf("unknown format %q", format)}
	}
	return nil
}

// probe is what the device reports about a path, after following symlinks.
type probe struct {
	resolved, kind, mode, owner string
	size                        int64
}

const probeScript = `p=$1
r=$(readlink -f -- "$p" 2>/dev/null) || r=$p
[ -n "$r" ] || r=$p
if [ -f "$r" ]; then
	set -- $(stat -c '%a %U:%G %s' -- "$r")
	printf 'file\n%s\n%s\n%s\n%s\n' "$r" "$1" "$2" "$3"
elif [ -e "$r" ]; then
	printf 'other\n%s\n' "$r"
else
	printf 'missing\n%s\n' "$r"
fi`

func (e *Editor) probe(ctx context.Context, p string) (probe, error) {
	out, err := e.run(ctx, probeScript, p)
	if err != nil {
		return probe{}, err
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) < 2 {
		return probe{}, fmt.Errorf("unexpected answer from the device: %q", out)
	}
	pr := probe{kind: lines[0], resolved: lines[1]}
	if _, err := CheckPath(pr.resolved); err != nil {
		return probe{}, err
	}
	if pr.kind == "file" {
		if len(lines) < 5 {
			return probe{}, fmt.Errorf("unexpected answer from the device: %q", out)
		}
		pr.mode, pr.owner = lines[2], lines[3]
		pr.size, _ = strconv.ParseInt(lines[4], 10, 64)
	}
	return pr, nil
}

// Read returns a file's content and metadata; a missing file is returned with Exists false.
func (e *Editor) Read(ctx context.Context, p string) (File, error) {
	p, err := CheckPath(p)
	if err != nil {
		return File{}, err
	}
	pr, err := e.probe(ctx, p)
	if err != nil {
		return File{}, err
	}
	switch pr.kind {
	case "missing":
		return File{Path: p}, nil
	case "file":
	default:
		return File{}, ErrNotRegular
	}
	if pr.size > MaxSize {
		return File{}, ErrTooLarge
	}
	data, err := e.Dev.Pull(ctx, pr.resolved, MaxSize)
	if errors.Is(err, adb.ErrTooLarge) {
		return File{}, ErrTooLarge
	}
	if err != nil {
		return File{}, err
	}
	if bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
		return File{}, ErrBinary
	}
	return File{Path: p, Exists: true, Content: string(data), Mode: pr.mode, Owner: pr.owner, Size: int64(len(data))}, nil
}

// replaceScript backs the target up, then replaces it atomically: the new content goes into a
// copy of the old file (keeping owner and mode) next to it, which is renamed over the target. The
// umask matters: adbd's shell may run with umask 0, which would leave new folders world-writable.
const replaceScript = `set -eu
umask 077
target=$1 staged=$2 backups=$3 keep=$4 newmode=$5 stamp=$6
mkdir -p "$backups"
chmod 700 "$backups" "$(dirname "$backups")" "$(dirname "$(dirname "$backups")")"
backup=
if [ -f "$target" ]; then
	backup=$stamp-$$
	cp -p "$target" "$backups/$backup"
	cp -p "$target" "$target.oxpf-tmp"
	cat "$staged" > "$target.oxpf-tmp"
elif [ -d "$(dirname "$target")" ]; then
	cp "$staged" "$target.oxpf-tmp"
	chmod "$newmode" "$target.oxpf-tmp"
else
	rm -f "$staged"
	echo "the folder $(dirname "$target") does not exist" >&2
	exit 3
fi
rm -f "$staged"
mv -f "$target.oxpf-tmp" "$target"
sync
n=$(ls -1 "$backups" | wc -l)
if [ "$n" -gt "$keep" ]; then
	ls -1 "$backups" | sort | head -n $((n - keep)) | while read -r f; do rm -f "$backups/$f"; done
fi
printf 'backup=%s\n' "$backup"`

// restoreScript puts a backup back the same atomic way, or removes a file that did not exist.
const restoreScript = `set -eu
umask 077
target=$1 backup=$2
if [ -n "$backup" ]; then
	cp -p "$backup" "$target.oxpf-tmp"
	mv -f "$target.oxpf-tmp" "$target"
else
	rm -f "$target"
fi
sync`

// healthScript restarts a unit and reports whether it stays active without automatic restarts.
const healthScript = `unit=$1 wait=$2
if ! systemctl restart "$unit"; then echo unhealthy; exit 0; fi
i=0
while [ "$i" -lt "$wait" ]; do
	sleep 1
	i=$((i + 1))
	state=$(systemctl is-active "$unit" 2>/dev/null || true)
	case $state in
	active | activating | reloading) ;;
	*) echo unhealthy; exit 0 ;;
	esac
done
state=$(systemctl is-active "$unit" 2>/dev/null || true)
n=$(systemctl show -p NRestarts --value "$unit" 2>/dev/null || echo 0)
if [ "$state" = active ] && [ "${n:-0}" = 0 ]; then echo healthy; else echo unhealthy; fi`

const journalScript = `journalctl -u "$1" -n 40 --no-pager -o cat 2>&1 | tail -c 8000`

// Write replaces a file, restarts the service when asked and rolls back if it does not stay up.
func (e *Editor) Write(ctx context.Context, req WriteRequest) (WriteResult, error) {
	p, err := CheckPath(req.Path)
	if err != nil {
		return WriteResult{}, err
	}
	if err := Validate(req.Format, req.Content); err != nil {
		return WriteResult{}, err
	}
	pr, err := e.probe(ctx, p)
	if err != nil {
		return WriteResult{}, err
	}
	if pr.kind == "other" {
		return WriteResult{}, ErrNotRegular
	}
	target := pr.resolved

	if _, err := e.run(ctx, `umask 077; mkdir -p "$1" && chmod 700 "$1" "$(dirname "$1")"`, e.StagingDir); err != nil {
		return WriteResult{}, fmt.Errorf("prepare upload: %w", err)
	}
	staged := fmt.Sprintf("%s/%d.new", e.StagingDir, time.Now().UnixNano())
	if err := e.Dev.Push(ctx, staged, []byte(req.Content), 0o600); err != nil {
		return WriteResult{}, fmt.Errorf("upload: %w", err)
	}
	newMode := "600"
	if strings.HasPrefix(target, "/etc/") {
		newMode = "644"
	}
	// Backups are named after this computer's clock: the device may have no real-time clock.
	stamp := time.Now().UTC().Format("20060102-150405.000000000")
	out, err := e.run(ctx, replaceScript, target, staged, e.backupDir(target), strconv.Itoa(e.Keep), newMode, stamp)
	if err != nil {
		_, _ = e.run(ctx, `rm -f "$1"`, staged)
		return WriteResult{}, fmt.Errorf("replace: %w", err)
	}
	var res WriteResult
	if b := strings.TrimPrefix(lastLine(out), "backup="); b != "" {
		res.Backup = b
	}
	if req.Service == "" {
		res.Healthy = true
		return res, nil
	}

	res.Restarted = true
	if res.Healthy, err = e.restart(ctx, req.Service); err != nil {
		return res, err
	}
	if res.Healthy {
		return res, nil
	}
	res.Log, _ = e.run(ctx, journalScript, req.Service)
	backup := ""
	if res.Backup != "" {
		backup = e.backupDir(target) + "/" + res.Backup
	}
	if _, err := e.run(ctx, restoreScript, target, backup); err != nil {
		return res, fmt.Errorf("the service did not stay up and restoring the previous file failed: %w", err)
	}
	res.RolledBack = true
	if _, err := e.restart(ctx, req.Service); err != nil {
		return res, err
	}
	return res, nil
}

// Backups lists the kept versions of a file, newest first.
func (e *Editor) Backups(ctx context.Context, p string) ([]Backup, error) {
	p, err := CheckPath(p)
	if err != nil {
		return nil, err
	}
	pr, err := e.probe(ctx, p)
	if err != nil {
		return nil, err
	}
	out, err := e.run(ctx, `[ -d "$1" ] && ls -1 "$1" || true`, e.backupDir(pr.resolved))
	if err != nil {
		return nil, err
	}
	var list []Backup
	for _, name := range strings.Fields(out) {
		if len(name) < 15 {
			continue
		}
		// Names are the computer's UTC time of the write, then the device shell's pid.
		t, err := time.Parse("20060102-150405", name[:15])
		if err != nil {
			continue
		}
		list = append(list, Backup{ID: name, Time: t})
	}
	slices.SortFunc(list, func(a, b Backup) int { return strings.Compare(b.ID, a.ID) })
	return list, nil
}

// Restore writes a kept version back, through Write, so the current version is kept in turn.
func (e *Editor) Restore(ctx context.Context, p, id, service string) (WriteResult, error) {
	p, err := CheckPath(p)
	if err != nil {
		return WriteResult{}, err
	}
	if id == "" || strings.ContainsAny(id, "/\x00") || id == "." || id == ".." {
		return WriteResult{}, errors.New("unknown backup")
	}
	pr, err := e.probe(ctx, p)
	if err != nil {
		return WriteResult{}, err
	}
	data, err := e.Dev.Pull(ctx, e.backupDir(pr.resolved)+"/"+id, MaxSize)
	if err != nil {
		return WriteResult{}, fmt.Errorf("read backup: %w", err)
	}
	return e.Write(ctx, WriteRequest{Path: p, Content: string(data), Format: "text", Service: service})
}

func (e *Editor) restart(ctx context.Context, service string) (bool, error) {
	out, err := e.run(ctx, healthScript, service, strconv.Itoa(e.HealthWait))
	if err != nil {
		return false, fmt.Errorf("restart %s: %w", service, err)
	}
	return lastLine(out) == "healthy", nil
}

// backupDir names a file's backup directory after its path: /etc/x.json -> etc%x.json.
func (e *Editor) backupDir(target string) string {
	return e.BackupRoot + "/" + strings.ReplaceAll(strings.TrimPrefix(target, "/"), "/", "%")
}

// run executes a script with positional arguments; a non-zero exit is an error carrying stderr.
func (e *Editor) run(ctx context.Context, script string, args ...string) (string, error) {
	var cmd strings.Builder
	cmd.WriteString("sh -c ")
	cmd.WriteString(quote(script))
	cmd.WriteString(" oxpf")
	for _, a := range args {
		cmd.WriteString(" ")
		cmd.WriteString(quote(a))
	}
	res, err := e.Dev.Shell(ctx, cmd.String())
	if err != nil {
		return "", err
	}
	// Older adbd versions run commands on a terminal, which turns every \n into \r\n.
	out := strings.ReplaceAll(string(res.Stdout), "\r\n", "\n")
	if res.ExitCode != 0 {
		msg := strings.TrimSpace(strings.ReplaceAll(string(res.Stderr), "\r\n", "\n"))
		if msg == "" {
			msg = strings.TrimSpace(out)
		}
		return "", fmt.Errorf("exit %d: %s", res.ExitCode, msg)
	}
	return out, nil
}

// quote makes s one single-quoted shell word.
func quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func lastLine(s string) string {
	s = strings.TrimRight(s, "\n")
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
}
