// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package identity

import (
	"archive/zip"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/asn1"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/stage"
)

func derPair(t *testing.T) Pair {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pk8, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	content := struct{ Type asn1.ObjectIdentifier }{asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 1}}
	signed, err := asn1.Marshal(struct {
		Version int
		Digests []asn1.RawValue `asn1:"set"`
		Content any
		Signers []asn1.RawValue `asn1:"set"`
	}{1, []asn1.RawValue{}, content, []asn1.RawValue{}})
	if err != nil {
		t.Fatal(err)
	}
	p7b, err := asn1.Marshal(struct {
		Type    asn1.ObjectIdentifier
		Content asn1.RawValue
	}{
		asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 2}, asn1.RawValue{Class: 2, Tag: 0, IsCompound: true, Bytes: signed},
	})
	if err != nil {
		t.Fatal(err)
	}
	return Pair{Key: pk8, Certificate: p7b}
}
func putPair(t *testing.T, p Pair) []string {
	t.Helper()
	dir := t.TempDir()
	paths := []string{filepath.Join(dir, "certificate.p7b"), filepath.Join(dir, "identity.pk8")}
	for i, b := range [][]byte{p.Certificate, p.Key} {
		if err := os.WriteFile(paths[i], b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return paths
}
func zipPair(t *testing.T, files map[string][]byte) string {
	t.Helper()
	name := filepath.Join(t.TempDir(), "selected.zip")
	f, err := os.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	for name, data := range files {
		e, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = e.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	return name
}
func TestGenericIdentityDERAndZIP(t *testing.T) {
	pair := derPair(t)
	for _, paths := range [][]string{putPair(t, pair), {zipPair(t, map[string][]byte{"nested/certificate.p7b": pair.Certificate, "identity.pk8": pair.Key, "ignored.txt": []byte("ignored")})}} {
		got, err := Read(paths)
		if err != nil || len(got.Key) != len(pair.Key) || len(got.Certificate) != len(pair.Certificate) {
			t.Fatalf("generic DER pair refused: %v", err)
		}
	}
}
func TestIdentityRefusals(t *testing.T) {
	pair := derPair(t)
	paths := putPair(t, pair)
	for _, tc := range []struct {
		name         string
		paths        []string
		code, detail string
	}{
		{"one file", paths[:1], "identity-missing", "identity.pk8"},
		{"no files", nil, "identity-missing", "identity.pk8"},
		{"ZIP without pair", []string{zipPair(t, map[string][]byte{"readme": []byte("nothing")})}, "identity-missing", "identity.pk8"},
		{"duplicate", append(paths, paths[0]), "identity-duplicate", "certificate.p7b"},
		{"bad key", putPair(t, Pair{Key: []byte("secret-do-not-echo"), Certificate: pair.Certificate}), "identity-key", "identity.pk8"},
		{"bad certificate", putPair(t, Pair{Key: pair.Key, Certificate: []byte("secret-do-not-echo")}), "identity-certificate", "certificate.p7b"},
		{"trailing DER", putPair(t, Pair{Key: pair.Key, Certificate: append(append([]byte{}, pair.Certificate...), 0)}), "identity-certificate", "certificate.p7b"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Read(tc.paths)
			var p *stage.Problem
			if !errors.As(err, &p) || p.Code != tc.code || p.Detail != tc.detail {
				t.Fatalf("refusal: %v", err)
			}
		})
	}
}
