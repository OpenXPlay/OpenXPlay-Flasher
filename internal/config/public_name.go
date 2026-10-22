// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package config

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/adb"
)

// PublicNameMaxSize and the validation rules mirror the Linux relay's public_name/config.rs.
const PublicNameMaxSize = 16 * 1024

// PublicNameView is the only representation of private configuration sent to the UI.
type PublicNameView struct {
	Exists                bool              `json:"exists"`
	Enabled               bool              `json:"enabled"`
	Provider              string            `json:"provider"`
	Name                  string            `json:"name"`
	TokenSet              bool              `json:"tokenSet"`
	AgreeLetsEncryptTerms bool              `json:"agreeLetsEncryptTerms"`
	Acme                  string            `json:"acme,omitempty"`
	Errors                map[string]string `json:"errors"`
}

// PublicNameUpdate is a form submission; an empty token preserves the device's saved token.
type PublicNameUpdate struct {
	Enabled               *bool   `json:"enabled,omitempty"`
	Provider              *string `json:"provider,omitempty"`
	Name                  *string `json:"name,omitempty"`
	Token                 string  `json:"token"`
	AgreeLetsEncryptTerms *bool   `json:"agreeLetsEncryptTerms,omitempty"`
}

// PrivateValidationError contains codes only, never input or JSON decoder errors.
type PrivateValidationError struct{ Fields map[string]string }

func (e *PrivateValidationError) Error() string { return "private configuration is not valid" }

var ErrPrivateOperation = errors.New("could not update or read private configuration on the device")

func validPublicName(name, provider string) bool {
	if len(name) > 253 {
		return false
	}
	labels := strings.Split(name, ".")
	if len(labels) < 2 {
		return false
	}
	for _, label := range labels {
		if len(label) < 1 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range []byte(label) {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return provider != "duckdns" || len(labels) == 3 && strings.HasSuffix(name, ".duckdns.org")
}
func validPublicToken(token, provider string) bool {
	if len(token) < 1 || len(token) > 256 {
		return false
	}
	for _, c := range []byte(token) {
		if c < 0x21 || c > 0x7e {
			return false
		}
	}
	if provider != "duckdns" {
		return true
	}
	groups := strings.Split(token, "-")
	if len(groups) != 5 {
		return false
	}
	for i, n := range []int{8, 4, 4, 4, 12} {
		if len(groups[i]) != n {
			return false
		}
		for _, c := range []byte(groups[i]) {
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
				return false
			}
		}
	}
	return true
}

// parsePublicName retains the original object in the backend only. Unknown keys are rejected;
// their names are not echoed because even a key in untrusted JSON can contain a secret.
func parsePublicName(data []byte) (PublicNameView, map[string]json.RawMessage) {
	v := PublicNameView{Exists: true, Enabled: true, Errors: map[string]string{}}
	var obj map[string]json.RawMessage
	if len(data) > PublicNameMaxSize {
		v.Errors["file"] = "too-large"
		return v, nil
	}
	if !utf8.Valid(data) || !validJSONSurrogates(data) || json.Unmarshal(data, &obj) != nil || obj == nil {
		v.Errors["file"] = "not-json"
		return v, nil
	}
	for k := range obj {
		switch k {
		case "version", "enabled", "provider", "name", "token", "agreeLetsEncryptTerms", "acme":
		default:
			v.Errors["file"] = "unknown-key"
		}
	}
	if b, ok := obj["version"]; !ok {
		v.Errors["version"] = "missing"
	} else if string(b) != "1" {
		v.Errors["version"] = "bad-version"
	}
	for k, dest := range map[string]*bool{"enabled": &v.Enabled, "agreeLetsEncryptTerms": &v.AgreeLetsEncryptTerms} {
		if b, ok := obj[k]; ok {
			if string(b) != "true" && string(b) != "false" {
				v.Errors[k] = "invalid"
			} else {
				_ = json.Unmarshal(b, dest)
			}
		}
	}
	for k, dest := range map[string]*string{"provider": &v.Provider, "name": &v.Name, "acme": &v.Acme} {
		if b, ok := obj[k]; ok {
			if len(b) == 0 || b[0] != '"' || json.Unmarshal(b, dest) != nil {
				v.Errors[k] = "invalid"
			}
		}
	}
	if _, ok := obj["provider"]; !ok {
		v.Errors["provider"] = "missing"
	} else if v.Provider != "duckdns" && v.Provider != "dynv6" {
		v.Errors["provider"] = "invalid"
		v.Provider = ""
	}
	if _, ok := obj["name"]; !ok {
		v.Errors["name"] = "missing"
	} else {
		ascii := true
		for _, c := range []byte(v.Name) {
			if c >= 128 {
				ascii = false
			}
		}
		v.Name = strings.ToLower(v.Name)
		if !ascii || !validPublicName(v.Name, v.Provider) {
			v.Errors["name"] = "invalid"
			v.Name = ""
		}
	}
	if _, ok := obj["acme"]; ok && v.Acme != "production" && v.Acme != "staging" {
		v.Errors["acme"] = "invalid"
		v.Acme = ""
	}
	var token string
	if b, ok := obj["token"]; !ok {
		v.Errors["token"] = "missing"
	} else if json.Unmarshal(b, &token) != nil || !validPublicToken(token, v.Provider) {
		v.Errors["token"] = "invalid"
	} else {
		v.TokenSet = true
	}
	return v, obj
}

// encoding/json replaces lone UTF-16 surrogates; serde_json rejects them instead.
func validJSONSurrogates(data []byte) bool {
	for i := 0; i < len(data); i++ {
		if data[i] != '\\' {
			continue
		}
		i++
		if i >= len(data) || data[i] != 'u' {
			continue
		}
		if i+4 >= len(data) {
			return false
		}
		n, err := strconv.ParseUint(string(data[i+1:i+5]), 16, 16)
		if err != nil {
			return false
		}
		i += 4
		if n >= 0xdc00 && n <= 0xdfff {
			return false
		}
		if n >= 0xd800 && n <= 0xdbff {
			if i+6 >= len(data) || data[i+1] != '\\' || data[i+2] != 'u' {
				return false
			}
			low, err := strconv.ParseUint(string(data[i+3:i+7]), 16, 16)
			if err != nil || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 6
		}
	}
	return true
}

func privateValidation(v PublicNameView) error {
	fields := map[string]string{}
	for k, c := range v.Errors {
		if c != "missing" || k == "version" {
			fields[k] = c
		}
	}
	if len(fields) > 0 {
		return &PrivateValidationError{fields}
	}
	return nil
}

// PublicNameRead never exposes the raw file, including on device failures.
func (e *Editor) PublicNameRead(ctx context.Context, p string) (PublicNameView, error) {
	f, err := e.readPublicNameFile(ctx, p)
	if err != nil {
		return PublicNameView{}, ErrPrivateOperation
	}
	if !f.Exists {
		return PublicNameView{Errors: map[string]string{}}, nil
	}
	v, _ := parsePublicName([]byte(f.Content))
	return v, nil
}

// decodePublicObject does only structural checks; field validation happens once on the final save.
func decodePublicObject(data []byte) (map[string]json.RawMessage, error) {
	if len(data) > PublicNameMaxSize {
		return nil, &PrivateValidationError{map[string]string{"file": "too-large"}}
	}
	var obj map[string]json.RawMessage
	if !utf8.Valid(data) || !validJSONSurrogates(data) || json.Unmarshal(data, &obj) != nil || obj == nil {
		return nil, &PrivateValidationError{map[string]string{"file": "not-json"}}
	}
	return obj, nil
}
func (e *Editor) publicNameObject(ctx context.Context, p string) (map[string]json.RawMessage, error) {
	f, err := e.readPublicNameFile(ctx, p)
	if err != nil {
		return nil, err
	}
	if !f.Exists {
		return map[string]json.RawMessage{"enabled": json.RawMessage("false")}, nil
	}
	return decodePublicObject([]byte(f.Content))
}

func (e *Editor) PublicNameWrite(ctx context.Context, p string, u PublicNameUpdate) (PublicNameView, error) {
	obj, err := e.publicNameObject(ctx, p)
	if err != nil {
		return PublicNameView{}, err
	}
	if _, ok := obj["version"]; !ok {
		obj["version"] = json.RawMessage("1")
	}
	changed := map[string]bool{}
	set := func(k string, v any) { obj[k], _ = json.Marshal(v); changed[k] = true }
	if u.Enabled != nil {
		set("enabled", *u.Enabled)
	}
	if u.AgreeLetsEncryptTerms != nil {
		set("agreeLetsEncryptTerms", *u.AgreeLetsEncryptTerms)
	}
	for k, value := range map[string]*string{"provider": u.Provider, "name": u.Name} {
		if value == nil {
			continue
		}
		changed[k] = true
		if *value == "" {
			delete(obj, k)
		} else if k == "name" {
			set(k, strings.TrimSpace(*value))
		} else {
			set(k, *value)
		}
	}
	if u.Token != "" {
		set("token", u.Token)
	}
	return e.writePublicName(ctx, p, obj, changed)
}

// PublicNameImport reads the existing file once and keeps unshown keys and an empty token.
func (e *Editor) PublicNameImport(ctx context.Context, p string, data []byte) (PublicNameView, error) {
	obj, err := decodePublicObject(data)
	if err != nil {
		return PublicNameView{}, err
	}
	old, err := e.publicNameObject(ctx, p)
	// A valid import can repair a malformed existing file; unreadable device files still fail.
	if err != nil {
		var invalid *PrivateValidationError
		if !errors.As(err, &invalid) {
			return PublicNameView{}, err
		}
	}
	changed := map[string]bool{"enabled": true, "provider": true, "name": true, "token": true, "agreeLetsEncryptTerms": true}
	if string(obj["token"]) == `""` {
		changed["token"] = false
		if token, ok := old["token"]; ok {
			obj["token"] = token
		} else {
			delete(obj, "token")
		}
	}
	for k, b := range old {
		if _, ok := obj[k]; !ok && k != "version" && k != "enabled" && k != "provider" && k != "name" && k != "token" && k != "agreeLetsEncryptTerms" {
			obj[k] = b
		}
	}
	return e.writePublicName(ctx, p, obj, changed)
}

// writePublicName validates the final upload once. Unchanged admin fields retain their bytes and
// status errors, matching apply_admin's rule that only submitted fields can block an update.
func (e *Editor) writePublicName(ctx context.Context, p string, obj map[string]json.RawMessage, changed map[string]bool) (PublicNameView, error) {
	data, _ := json.MarshalIndent(obj, "", "  ")
	data = append(data, '\n')
	v, _ := parsePublicName(data)
	check := v
	check.Errors = map[string]string{}
	for k, code := range v.Errors {
		if (k == "token" || k == "name" || k == "provider" || k == "enabled" || k == "agreeLetsEncryptTerms") && !changed[k] {
			continue
		}
		check.Errors[k] = code
	}
	if err := privateValidation(check); err != nil {
		return PublicNameView{}, err
	}
	if changed["name"] && v.Name != "" {
		obj["name"], _ = json.Marshal(v.Name)
		data, _ = json.MarshalIndent(obj, "", "  ")
		data = append(data, '\n')
	}
	if _, err := e.writeValidated(ctx, WriteRequest{Path: p, Content: string(data), Format: "public-name"}); err != nil {
		return PublicNameView{}, ErrPrivateOperation
	}
	return v, nil
}

// PublicNameRestore keeps backup contents and errors away from the browser.
func (e *Editor) PublicNameRestore(ctx context.Context, p, id string) (PublicNameView, error) {
	if !validBackupID(id) {
		return PublicNameView{}, ErrPrivateOperation
	}
	pr, err := e.probe(ctx, p)
	if err != nil {
		return PublicNameView{}, ErrPrivateOperation
	}
	data, err := e.readBackup(ctx, pr.resolved, id, PublicNameMaxSize)
	if err != nil {
		return PublicNameView{}, ErrPrivateOperation
	}
	obj, err := decodePublicObject(data)
	if err != nil {
		return PublicNameView{}, err
	}
	return e.writePublicName(ctx, p, obj, map[string]bool{"enabled": true, "provider": true, "name": true, "token": true, "agreeLetsEncryptTerms": true})
}

// PublicNameRemove irreversibly removes the private file and both layouts of its backups.
func (e *Editor) PublicNameRemove(ctx context.Context, p string) error {
	if _, err := CheckPath(p); err != nil {
		return ErrPrivateOperation
	}
	pr, err := e.probe(ctx, p)
	if err != nil {
		return ErrPrivateOperation
	}
	if (pr.kind != "file" && pr.kind != "missing") || pr.size > MaxSize {
		return ErrPrivateOperation
	}
	if err := e.checkBackupDir(ctx, pr.resolved, false); err != nil {
		return ErrPrivateOperation
	}
	_, err = e.run(ctx, removePrivateScript, pr.resolved, e.backupDir(pr.resolved), e.legacyBackupDir(pr.resolved))
	if err != nil {
		return ErrPrivateOperation
	}
	return nil
}

const removePrivateScript = `set -eu
target=$1 backups=$2 legacy=$3
# Delete backups first: a failure must not be reported as successful removal.
rm -rf -- "$backups" "$legacy"
rm -f -- "$target"
sync
for p in "$target" "$backups" "$legacy"; do
 [ ! -e "$p" ] && [ ! -L "$p" ] || { echo 'private configuration removal failed' >&2; exit 3; }
done`

func (e *Editor) readPublicNameFile(ctx context.Context, p string) (File, error) {
	if _, err := CheckPath(p); err != nil {
		return File{}, ErrPrivateOperation
	}
	pr, err := e.probe(ctx, p)
	if err != nil {
		return File{}, ErrPrivateOperation
	}
	if pr.kind == "missing" {
		return File{Path: p}, nil
	}
	if pr.kind != "file" {
		return File{}, ErrPrivateOperation
	}
	if pr.size > PublicNameMaxSize {
		return File{Exists: true, Content: strings.Repeat(" ", PublicNameMaxSize+1)}, nil
	}
	data, err := e.Dev.Pull(ctx, pr.resolved, PublicNameMaxSize)
	if errors.Is(err, adb.ErrTooLarge) {
		return File{Exists: true, Content: strings.Repeat(" ", PublicNameMaxSize+1)}, nil
	}
	if err != nil {
		return File{}, ErrPrivateOperation
	}
	return File{Path: p, Exists: true, Content: string(data)}, nil
}
