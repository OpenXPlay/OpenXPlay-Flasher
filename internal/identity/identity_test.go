// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package identity

import (
	"archive/zip"
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/asn1"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/stage"
)

func derPair(t *testing.T) Pair {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
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
		{"bad key", putPair(t, Pair{Key: []byte("secret-do-not-echo"), Certificate: pair.Certificate}), "identity-unrecognized", ""},
		{"bad certificate", putPair(t, Pair{Key: pair.Key, Certificate: []byte("secret-do-not-echo")}), "identity-unrecognized", ""},
		{"trailing DER", putPair(t, Pair{Key: pair.Key, Certificate: append(append([]byte{}, pair.Certificate...), 0)}), "identity-unrecognized", ""},
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

func TestAnyNamesEitherOrderAndPEMAlwaysBecomeDER(t *testing.T) {
	pair := derPair(t)
	for _, label := range []string{"PKCS7", "CMS"} {
		for _, reverse := range []bool{false, true} {
			dir := t.TempDir()
			paths := []string{filepath.Join(dir, "not-the-key"), filepath.Join(dir, "not-the-cert")}
			os.WriteFile(paths[0], pem.EncodeToMemory(&pem.Block{Type: label, Bytes: pair.Certificate}), 0o600)
			os.WriteFile(paths[1], pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pair.Key}), 0o600)
			if reverse {
				slices.Reverse(paths)
			}
			got, err := Read(paths)
			if err != nil || !bytes.Equal(got.Key, pair.Key) || !bytes.Equal(got.Certificate, pair.Certificate) {
				t.Fatalf("%s reverse=%t: %v", label, reverse, err)
			}
			clear(got.Key)
			clear(got.Certificate)
		}
	}
	paths := putPair(t, Pair{Key: pair.Certificate, Certificate: pair.Key})
	if _, err := Read(paths); err != nil {
		t.Fatalf("filenames classified contents: %v", err)
	}
}

func TestPrivateKeyMustBeP256PKCS8(t *testing.T) {
	pair := derPair(t)
	ec, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	sec1, _ := x509.MarshalECPrivateKey(ec)
	otherCurve, _ := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	p384, _ := x509.MarshalPKCS8PrivateKey(otherCurve)
	ed := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	edDER, _ := x509.MarshalPKCS8PrivateKey(ed)
	for _, tc := range []struct {
		name string
		data []byte
		code string
	}{
		{"SEC1 PEM", pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: sec1}), "identity-key-format"},
		{"SEC1 DER", sec1, "identity-key-format"},
		{"P384", p384, "identity-key-curve"},
		{"Ed25519", edDER, "identity-key-type"},
		{"malformed PRIVATE KEY", pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("do-not-echo-secret")}), "identity-key"},
		{"malformed CMS", pem.EncodeToMemory(&pem.Block{Type: "CMS", Bytes: []byte("do-not-echo-secret")}), "identity-certificate"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Read(putPair(t, Pair{Key: tc.data, Certificate: pair.Certificate}))
			var p *stage.Problem
			if !errors.As(err, &p) || p.Code != tc.code || strings.Contains(err.Error(), "do-not-echo-secret") {
				t.Fatalf("unsafe or wrong refusal: %v", err)
			}
		})
	}
}

func TestZIPNamesCaseFallbackAndNeverScansOtherContents(t *testing.T) {
	pair := derPair(t)
	for _, files := range []map[string][]byte{
		{"nested/IDENTITY.PK8": pair.Key, "other/CERTIFICATE.P7B": pair.Certificate, "ignored/extra.pk8": []byte("ignore")},
		{"nested/custom.PK8": pair.Key, "other/custom.P7B": pair.Certificate, "ignored.txt": pair.Key},
	} {
		original := zipPair(t, files)
		filename := filepath.Join(t.TempDir(), "arbitrary container name")
		data, _ := os.ReadFile(original)
		os.WriteFile(filename, data, 0o600)
		got, err := Read([]string{filename})
		if err != nil || !bytes.Equal(got.Key, pair.Key) {
			t.Fatalf("named ZIP refused: %v", err)
		}
	}
	for _, tc := range []struct {
		files        map[string][]byte
		code, detail string
	}{
		{map[string][]byte{"unrelated.txt": pair.Key, "other.txt": pair.Certificate}, "identity-missing", "identity.pk8"},
		{map[string][]byte{"a/identity.pk8": pair.Key, "b/IDENTITY.PK8": pair.Key, "certificate.p7b": pair.Certificate}, "identity-duplicate", "identity.pk8"},
		{map[string][]byte{"a.pk8": pair.Key, "b.PK8": pair.Key, "a.p7b": pair.Certificate}, "identity-duplicate", "identity.pk8"},
		{map[string][]byte{"identity.pk8": make([]byte, MaxFile+1), "certificate.p7b": pair.Certificate}, "identity-read", "identity.pk8"},
	} {
		_, err := Read([]string{zipPair(t, tc.files)})
		var p *stage.Problem
		if !errors.As(err, &p) || p.Code != tc.code || p.Detail != tc.detail {
			t.Fatalf("ZIP refusal: %v", err)
		}
	}
	filename := filepath.Join(t.TempDir(), "encrypted.zip")
	f, _ := os.Create(filename)
	w := zip.NewWriter(f)
	entry, _ := w.CreateHeader(&zip.FileHeader{Name: "identity.pk8", Flags: 1, Method: zip.Store})
	entry.Write(pair.Key)
	w.Close()
	f.Close()
	_, err := Read([]string{filename})
	var p *stage.Problem
	if !errors.As(err, &p) || p.Code != "identity-encrypted" {
		t.Fatalf("encrypted entry: %v", err)
	}
}
