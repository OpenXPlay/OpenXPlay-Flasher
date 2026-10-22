// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package config

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPublicNameRelayRules(t *testing.T) {
	for _, c := range []struct{ content, key, code string }{
		{`{}`, "version", "missing"},
		{`{"version":1}`, "provider", "missing"},
		{`{"version":1.0}`, "version", "bad-version"},
		{`{"version":2}`, "version", "bad-version"},
		{`{"version":1,"enabled":null}`, "enabled", "invalid"},
		{`{"version":1,"agreeLetsEncryptTerms":1}`, "agreeLetsEncryptTerms", "invalid"},
		{`{"version":1,"provider":"desec"}`, "provider", "invalid"},
		{`{"version":1,"provider":"duckdns","name":"a.b.duckdns.org"}`, "name", "invalid"},
		{`{"version":1,"provider":"dynv6","name":"K.example.net"}`, "name", "invalid"},
		{`{"version":1,"name":"a.example.net."}`, "name", "invalid"},
		{`{"version":1,"name":"-a.example.net"}`, "name", "invalid"},
		{`{"version":1,"provider":"duckdns","token":"abc"}`, "token", "invalid"},
		{`{"version":1,"token":"with space"}`, "token", "invalid"},
		{`{"version":1,"token":null}`, "token", "invalid"},
		{`{"version":1,"acme":"test"}`, "acme", "invalid"},
		{`{"version":1,"typo":true}`, "file", "unknown-key"},
		{`{"version":1,"token":"\ud800"}`, "file", "not-json"},
		{`{"version":1,"token":"\udc00"}`, "file", "not-json"},
		{`[]`, "file", "not-json"}, {`null`, "file", "not-json"},
		{`{} {}`, "file", "not-json"}, {"{\"token\":\"\xff\"}", "file", "not-json"},
		{strings.Repeat(" ", PublicNameMaxSize+1), "file", "too-large"},
	} {
		v, _ := parsePublicName([]byte(c.content))
		if v.Errors[c.key] != c.code {
			t.Errorf("rule for %s: wanted %s, got %s", c.key, c.code, v.Errors[c.key])
		}
	}
	for _, provider := range []string{"duckdns", "dynv6"} {
		name := "MyRelay.DuckDNS.org"
		token := "a1b2c3d4-0000-0000-0000-000000000000"
		if provider == "dynv6" {
			name = "Car-1.example.net"
			token = "Zs1x_Token-.~"
		}
		data, _ := json.Marshal(map[string]any{"version": 1, "provider": provider, "name": name, "token": token})
		v, _ := parsePublicName(data)
		if len(v.Errors) != 0 || !v.Enabled || !v.TokenSet || v.Name != strings.ToLower(name) || v.AgreeLetsEncryptTerms {
			t.Fatal("valid relay file rejected or terms assumed")
		}
	}
	if !validPublicToken(strings.Repeat("x", 256), "dynv6") || validPublicToken(strings.Repeat("x", 257), "dynv6") || validPublicName(strings.Repeat("a", 64)+".example.net", "dynv6") {
		t.Fatal("boundary rules differ from relay")
	}
}

func TestPublicNameFormKeepsTokenAndHiddenKeys(t *testing.T) {
	ed, dev, p := fixture(t, "")
	ctx := context.Background()
	token := strings.Repeat("s", 31)
	content, _ := json.Marshal(map[string]any{"version": 1, "provider": "dynv6", "name": "car.example.net", "token": token, "acme": "staging", "agreeLetsEncryptTerms": true})
	if err := os.WriteFile(p, content, 0o644); err != nil {
		t.Fatal(err)
	}
	update := PublicNameUpdate{Enabled: testPtr(true), Provider: testPtr("dynv6"), Name: testPtr("CAR.example.net"), AgreeLetsEncryptTerms: testPtr(false)}
	v, err := ed.PublicNameWrite(ctx, p, update)
	if err != nil || !v.TokenSet || v.Name != "car.example.net" || v.AgreeLetsEncryptTerms || v.Acme != "staging" {
		t.Fatal("form update failed or changed hidden state")
	}
	data, _ := os.ReadFile(p)
	var obj map[string]any
	_ = json.Unmarshal(data, &obj)
	if obj["token"] != token || obj["acme"] != "staging" || obj["version"] != float64(1) {
		t.Fatal("saved credentials or hidden keys changed")
	}
	st, _ := os.Stat(p)
	if st.Mode().Perm() != 0o600 {
		t.Fatal("privacy file not mode 0600")
	}
	response, _ := json.Marshal(v)
	if strings.Contains(string(response), token) || strings.Contains(string(response), `"token":`) {
		t.Fatal("response exposed a saved token")
	}
	for _, cmd := range dev.shells {
		if strings.Contains(cmd, token) || strings.Contains(cmd, "systemctl restart") {
			t.Fatal("secret in command or privacy restarted relay")
		}
	}
	// Existing unknown keys must survive a rejected edit rather than silently disappearing.
	obj["future"] = map[string]any{"extra": 1}
	original, _ := json.Marshal(obj)
	_ = os.WriteFile(p, original, 0o600)
	_, err = ed.PublicNameWrite(ctx, p, update)
	var invalid *PrivateValidationError
	if !errors.As(err, &invalid) || invalid.Fields["file"] != "unknown-key" {
		t.Fatal("unknown relay key was accepted")
	}
	after, _ := os.ReadFile(p)
	if string(after) != string(original) {
		t.Fatal("unknown key lost on failed save")
	}
}

func TestPublicNameCreateImportRemoveRestore(t *testing.T) {
	ed, _, p := fixture(t, "")
	ctx := context.Background()
	v, err := ed.PublicNameRead(ctx, p)
	if err != nil || v.Exists || v.Enabled || v.AgreeLetsEncryptTerms {
		t.Fatal("missing file should start off and unagreed")
	}
	v, err = ed.PublicNameWrite(ctx, p, PublicNameUpdate{Enabled: testPtr(true)})
	if err != nil || !v.Exists || v.TokenSet || v.Errors["token"] != "missing" {
		t.Fatal("partial form must save without agreeing")
	}
	st, _ := os.Stat(p)
	if st.Mode().Perm() != 0o600 {
		t.Fatal("new privacy file not 0600")
	}
	data := []byte(`{"version":1,"provider":"dynv6","name":"car.example.net","token":"abc","acme":"staging"}`)
	v, err = ed.PublicNameImport(ctx, p, data)
	if err != nil || !v.TokenSet || !v.Enabled || v.AgreeLetsEncryptTerms {
		t.Fatal("valid import failed or consent assumed")
	}
	v, err = ed.PublicNameImport(ctx, p, []byte(`{"version":1,"provider":"dynv6","name":"car.example.net","token":""}`))
	if err != nil || !v.TokenSet || v.Acme != "staging" {
		t.Fatal("import did not preserve blank token and hidden acme")
	}
	for _, bad := range []string{`{"version":2}`, `{"version":1,"token":"bad token"}`, `{"version":1,"acme":"test"}`, `{"version":1,"unknown":"x"}`, strings.Repeat("x", PublicNameMaxSize+1)} {
		if _, err := ed.PublicNameImport(ctx, p, []byte(bad)); err == nil {
			t.Fatal("invalid import accepted")
		}
	}
	list, err := ed.Backups(ctx, p)
	if err != nil || len(list) == 0 {
		t.Fatal("ordinary saves must keep backups")
	}
	v, err = ed.PublicNameRestore(ctx, p, list[0].ID)
	if err != nil || !v.Exists || !v.TokenSet || v.Acme != "staging" {
		t.Fatal("private restore failed")
	}
	if _, err := ed.PublicNameRestore(ctx, p, "../../escape"); err == nil {
		t.Fatal("restore accepted path traversal")
	}
	if err := ed.PublicNameRemove(ctx, p); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("Remove did not unlink file")
	}
	list, err = ed.Backups(ctx, p)
	if err != nil || len(list) != 0 {
		t.Fatal("Remove left earlier versions")
	}
	if err := ed.PublicNameRemove(ctx, p); err != nil {
		t.Fatal("removing a missing file should succeed")
	}
}

func TestRawPrivacyGateIncludesSymlinksAndStorage(t *testing.T) {
	ed, _, p := fixture(t, `{"token":"secret"}`)
	ctx := context.Background()
	for _, dir := range []string{ed.BackupRoot, ed.StagingDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	aliases := []string{}
	for i, target := range []string{p, ed.BackupRoot, ed.StagingDir} {
		link := filepath.Join(filepath.Dir(p), "alias"+string(rune('a'+i)))
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		aliases = append(aliases, link)
		if i > 0 {
			aliases = append(aliases, link+"/file")
		}
	}
	for _, path := range append([]string{p, ed.BackupRoot, ed.BackupRoot + "/file", ed.StagingDir, ed.StagingDir + "/file"}, aliases...) {
		if err := ed.CheckRawPath(ctx, path, []string{p}); !errors.Is(err, ErrPrivacy) {
			t.Errorf("raw privacy path was not refused: %s", path)
		}
	}
	if err := ed.CheckRawPath(ctx, p+".other", []string{p}); err != nil {
		t.Fatal("nonprivate sibling refused")
	}
	link := filepath.Join(filepath.Dir(p), "protected-link")
	_ = os.Symlink("/dev", link)
	if err := ed.CheckRawPath(ctx, link, []string{p}); !errors.Is(err, ErrProtected) {
		t.Fatal("protected symlink bypassed")
	}
}

func TestPrivateJSONErrorsNeverEchoInput(t *testing.T) {
	secret := strings.Repeat("credential", 4)
	for _, content := range []string{`{"version":1,"token":"` + secret + ` "}`, `{"version":1,"` + secret + `":true}`, `{"token":"` + secret} {
		v, _ := parsePublicName([]byte(content))
		response, _ := json.Marshal(v)
		err := privateValidation(v)
		if strings.Contains(string(response), secret) || err != nil && strings.Contains(err.Error(), secret) {
			t.Fatal("credential leaked in redacted view or validation error")
		}
	}
}

func testPtr[T any](v T) *T { return &v }

func TestFormRepairsMissingVersionAndKeepsUnsubmittedToken(t *testing.T) {
	ed, _, p := fixture(t, `{"enabled":true,"provider":"dynv6","name":"car.duckdns.org","token":"old-dynv6-token","agreeLetsEncryptTerms":true}`)
	ctx := context.Background()
	v, err := ed.PublicNameWrite(ctx, p, PublicNameUpdate{Provider: testPtr("duckdns")})
	if err != nil || v.Errors["token"] != "invalid" {
		t.Fatal("kept token blocked provider edit or its status error was lost")
	}
	v, err = ed.PublicNameWrite(ctx, p, PublicNameUpdate{Enabled: testPtr(false)})
	if err != nil || v.Enabled {
		t.Fatal("kept invalid token blocked disabling")
	}
	data, _ := os.ReadFile(p)
	var obj map[string]any
	_ = json.Unmarshal(data, &obj)
	if obj["version"] != float64(1) || obj["token"] != "old-dynv6-token" {
		t.Fatal("version was not repaired or kept token was modified")
	}
	if _, err := ed.PublicNameWrite(ctx, p, PublicNameUpdate{Token: "bad-new-token"}); err == nil {
		t.Fatal("new invalid token accepted")
	}
	// A relay admin may withdraw consent after the UI loaded. A name-only patch must keep false.
	obj["agreeLetsEncryptTerms"] = false
	data, _ = json.Marshal(obj)
	_ = os.WriteFile(p, data, 0o600)
	v, err = ed.PublicNameWrite(ctx, p, PublicNameUpdate{Name: testPtr("other.duckdns.org")})
	if err != nil || v.AgreeLetsEncryptTerms {
		t.Fatal("partial save overwrote withdrawn consent")
	}
	v, err = ed.PublicNameWrite(ctx, p, PublicNameUpdate{AgreeLetsEncryptTerms: testPtr(true)})
	if err != nil || !v.AgreeLetsEncryptTerms {
		t.Fatal("explicit consent edit was ignored")
	}
}

func TestBackupEncodingLegacyRestoreAndPrivacyAliases(t *testing.T) {
	ed, _, p := fixture(t, `{"version":1,"token":"kept"}`)
	ctx := context.Background()
	resolved, _ := filepath.EvalSymlinks(p)
	paths := []string{"/a/b", "/a%b", "/a%25b", "/a/25b", "/v2/example"}
	seen := map[string]bool{}
	for _, path := range paths {
		dir := ed.backupDir(path)
		if seen[dir] {
			t.Fatal("backup encoding collision")
		}
		seen[dir] = true
		if dir == ed.legacyBackupDir(path) || !strings.HasPrefix(dir, ed.BackupRoot+"/v2/") {
			t.Fatal("new backup namespace overlaps legacy names")
		}
	}
	legacy := ed.legacyBackupDir(resolved)
	if err := os.MkdirAll(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	const id = "20261020-010203-123"
	if err := os.WriteFile(filepath.Join(legacy, id), []byte(`{"version":1,"enabled":false}`), 0o600); err != nil {
		t.Fatal(err)
	}
	list, err := ed.Backups(ctx, p)
	if err != nil || len(list) != 1 || list[0].ID != id {
		t.Fatal("legacy backup not listed")
	}
	if _, err := ed.Restore(ctx, p, id, ""); err != nil {
		t.Fatal("legacy backup could not be restored")
	}
	data, _ := os.ReadFile(p)
	if string(data) != `{"version":1,"enabled":false}` {
		t.Fatal("legacy bytes not restored")
	}
	list, err = ed.Backups(ctx, p)
	if err != nil || len(list) != 2 {
		t.Fatal("legacy and new backup lists were not combined")
	}
	alias := "/" + strings.ReplaceAll(strings.TrimPrefix(resolved, "/"), "/", "%")
	if ed.legacyBackupDir(alias) != ed.legacyBackupDir(resolved) {
		t.Fatal("test alias does not reproduce old collision")
	}
	if err := ed.CheckRawPath(ctx, alias, []string{p}); !errors.Is(err, ErrPrivacy) {
		t.Fatal("legacy collision can still read private backups")
	}
	for _, path := range []string{"/var%lib%openxplay%public-name.json", "/var/lib%openxplay/public-name.json"} {
		if err := ed.CheckRawPath(ctx, path, []string{"/var/lib/openxplay/public-name.json"}); !errors.Is(err, ErrPrivacy) {
			t.Fatal("public-name legacy backup alias was permitted")
		}
	}
}

func TestPrivateProfileRootsPrefixesAndSymlinks(t *testing.T) {
	ed, _, p := fixture(t, "")
	base := filepath.Dir(p)
	root := filepath.Join(base, "private-store")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(root, "account.key")
	_ = os.WriteFile(key, []byte("key"), 0o600)
	tmp := filepath.Join(base, "public-name.tmp-abcd")
	_ = os.WriteFile(tmp, []byte("token"), 0o600)
	link := filepath.Join(base, "alias-key")
	_ = os.Symlink(key, link)
	tmplink := filepath.Join(base, "alias-temp")
	_ = os.Symlink(tmp, tmplink)
	roots := []string{root, filepath.Join(base, "public-name.tmp-*"), p + ".oxpf-tmp"}
	for _, path := range []string{root, key, link, tmp, tmplink, tmp + ".child", p + ".oxpf-tmp"} {
		if err := ed.CheckRawPath(context.Background(), path, roots); !errors.Is(err, ErrPrivacy) {
			t.Errorf("private profile path permitted: %s", path)
		}
	}
	if err := ed.CheckRawPath(context.Background(), filepath.Join(base, "public-name.tmp-notprivate-other"), []string{root}); err != nil {
		t.Fatal("unlisted sibling refused")
	}
}

// countedPullDevice proves that import reads the current file only once.
type countedPullDevice struct {
	*localDevice
	pulls int
}

func (d *countedPullDevice) Pull(ctx context.Context, p string, max int64) ([]byte, error) {
	d.pulls++
	return d.localDevice.Pull(ctx, p, max)
}
func TestPrivateImportReadsCurrentOnce(t *testing.T) {
	ed, dev, p := fixture(t, `{"version":1,"provider":"dynv6","name":"car.example.net","token":"old","acme":"staging"}`)
	counted := &countedPullDevice{localDevice: dev}
	ed.Dev = counted
	v, err := ed.PublicNameImport(context.Background(), p, []byte(`{"version":1,"provider":"dynv6","name":"car.example.net","token":""}`))
	if err != nil || !v.TokenSet || v.Acme != "staging" || counted.pulls != 1 {
		t.Fatal("import did not retain saved state in one read")
	}
}

func TestResolvedPrivateTemporarySiblingIsRefused(t *testing.T) {
	ed, _, p := fixture(t, `{}`)
	ctx := context.Background()
	link := filepath.Join(filepath.Dir(p), "private-link.json")
	if err := os.Symlink(p, link); err != nil {
		t.Fatal(err)
	}
	resolved, _ := filepath.EvalSymlinks(p)
	temp := resolved + ".oxpf-tmp"
	if err := os.WriteFile(temp, []byte("private data"), 0o600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(filepath.Dir(p), "temp-link")
	if err := os.Symlink(temp, alias); err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []string{temp, alias, link + ".oxpf-tmp"} {
		if err := ed.CheckRawPath(ctx, candidate, []string{link}); !errors.Is(err, ErrPrivacy) {
			t.Errorf("derived private temporary path permitted: %s", candidate)
		}
	}
}

func TestLongBackupPathsAndIdentityChecks(t *testing.T) {
	ed, _, original := fixture(t, "")
	ctx := context.Background()
	// The original path exceeds one filename, but every actual path component is short.
	dir := filepath.Join(filepath.Dir(original), strings.Repeat("a", 90), strings.Repeat("b", 90))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "settings.json")
	if len(p) < 250 {
		t.Fatal("fixture path is not long enough")
	}
	for _, content := range []string{"first", "second"} {
		if _, err := ed.Write(ctx, WriteRequest{Path: p, Content: content, Format: "text"}); err != nil {
			t.Fatal(err)
		}
	}
	resolved, _ := filepath.EvalSymlinks(p)
	backupDir := ed.backupDir(resolved)
	if len(filepath.Base(backupDir)) != 64 {
		t.Fatal("backup directory name is not a fixed-length digest")
	}
	marker := filepath.Join(backupDir, ".path")
	recorded, err := os.ReadFile(marker)
	if err != nil || string(recorded) != resolved {
		t.Fatal("original resolved path was not recorded")
	}
	if st, err := os.Stat(marker); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatal("backup path identity must be private")
	}
	list, err := ed.Backups(ctx, p)
	if err != nil || len(list) != 1 {
		t.Fatal("long-path backup listing failed")
	}
	if _, err := ed.Restore(ctx, p, list[0].ID, ""); err != nil {
		t.Fatal("long-path backup restore failed")
	}
	data, _ := os.ReadFile(p)
	if string(data) != "first" {
		t.Fatal("long-path backup did not restore the old bytes")
	}
	// A wrong or absent path marker must fail closed, including a write to that directory.
	for _, tamper := range []func(){
		func() { _ = os.WriteFile(marker, []byte("/different-file"), 0o600) },
		func() { _ = os.Remove(marker) },
	} {
		tamper()
		if _, err := ed.Backups(ctx, p); err == nil {
			t.Fatal("list accepted a missing or mismatched path identity")
		}
		if _, err := ed.Restore(ctx, p, list[0].ID, ""); err == nil {
			t.Fatal("restore accepted a missing or mismatched path identity")
		}
		if _, err := ed.Write(ctx, WriteRequest{Path: p, Content: "third", Format: "text"}); err == nil {
			t.Fatal("write accepted a missing or mismatched path identity")
		}
	}
}

func TestPrivateRemovePurgesBothBackupLayouts(t *testing.T) {
	ed, _, p := fixture(t, `{"version":1,"provider":"dynv6","name":"car.example.net","token":"saved-token"}`)
	ctx := context.Background()
	if _, err := ed.PublicNameWrite(ctx, p, PublicNameUpdate{Enabled: testPtr(false)}); err != nil {
		t.Fatal(err)
	}
	resolved, _ := filepath.EvalSymlinks(p)
	legacy := ed.legacyBackupDir(resolved)
	if err := os.MkdirAll(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "20261020-010203-123"), []byte("old private configuration"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := ed.Backups(ctx, p)
	if err != nil || len(before) != 2 {
		t.Fatal("fixture must contain both backup layouts")
	}
	if err := ed.PublicNameRemove(ctx, p); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{p, ed.backupDir(resolved), legacy} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatal("Remove left private file or backup directory")
		}
	}
	after, err := ed.Backups(ctx, p)
	if err != nil || len(after) != 0 {
		t.Fatal("Remove did not empty the earlier-versions list")
	}
	// Backups also need purging when the relay's file was already deleted by another administrator.
	if err := os.MkdirAll(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "20261020-010203-123"), []byte("orphaned private backup"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ed.PublicNameRemove(ctx, p); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatal("Remove left orphaned backups of a missing file")
	}
}

func TestPrivateRemoveReportsBackupDeletionFailure(t *testing.T) {
	ed, dev, p := fixture(t, `{"version":1,"token":"private-token"}`)
	ctx := context.Background()
	if _, err := ed.PublicNameWrite(ctx, p, PublicNameUpdate{Enabled: testPtr(false)}); err != nil {
		t.Fatal(err)
	}
	resolved, _ := filepath.EvalSymlinks(p)
	// Simulate a device filesystem refusing backup deletion, using the actual removal script.
	script := "#!/bin/sh\nfor arg do\n case \"$arg\" in " + quote(ed.backupDir(resolved)) + "|" + quote(ed.legacyBackupDir(resolved)) + ") echo 'backup deletion denied' >&2; exit 1;; esac\ndone\nexec /bin/rm \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dev.bin, "rm"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ed.PublicNameRemove(ctx, p); !errors.Is(err, ErrPrivateOperation) {
		t.Fatal("failed backup deletion was reported as success")
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatal("file was removed despite backup deletion failure")
	}
	backups, err := ed.Backups(ctx, p)
	if err != nil || len(backups) == 0 {
		t.Fatal("failure fixture did not retain its backup")
	}
}
