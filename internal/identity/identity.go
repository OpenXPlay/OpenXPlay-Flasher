// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

// Package identity classifies user-selected credentials using generic key and CMS syntax.
// It never persists or logs their contents; callers clear the returned DER after sending it.
package identity

import (
	"archive/zip"
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/x509"
	"encoding/asn1"
	"encoding/pem"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/stage"
)

const MaxFile = 4 << 20
const maxZIPEntries = 20000

type Pair struct{ Key, Certificate []byte }

func problem(code, name string) error { return &stage.Problem{Code: code, Detail: name} }

// Read classifies plain files by content in any order. ZIPs are recognized by their signature,
// and only entries selected by the documented name rules are ever opened.
func Read(paths []string) (pair Pair, result error) {
	defer func() {
		if result != nil {
			clear(pair.Key)
			clear(pair.Certificate)
			pair = Pair{}
		}
	}()
	add := func(data []byte, expected string) error {
		defer clear(data)
		kind, der, err := classify(data)
		if err != nil {
			return err
		}
		if expected != "" && kind != expected {
			clear(der)
			if expected == "identity.pk8" {
				return problem("identity-key", "")
			}
			return problem("identity-certificate", "")
		}
		if kind == "identity.pk8" {
			if pair.Key != nil {
				clear(der)
				return problem("identity-duplicate", kind)
			}
			pair.Key = der
		} else {
			if pair.Certificate != nil {
				clear(der)
				return problem("identity-duplicate", kind)
			}
			pair.Certificate = der
		}
		return nil
	}
	for _, filename := range paths {
		f, err := os.Open(filename)
		if err != nil {
			return pair, problem("identity-read", filepath.Base(filename))
		}
		err = func() error {
			defer f.Close()
			st, err := f.Stat()
			if err != nil || !st.Mode().IsRegular() {
				return problem("identity-read", filepath.Base(filename))
			}
			var magic [4]byte
			n, _ := f.ReadAt(magic[:], 0)
			isZIP := n == 4 && (bytes.Equal(magic[:], []byte{'P', 'K', 3, 4}) || bytes.Equal(magic[:], []byte{'P', 'K', 5, 6}) || bytes.Equal(magic[:], []byte{'P', 'K', 7, 8}))
			if !isZIP {
				if st.Size() > MaxFile {
					return problem("identity-read", filepath.Base(filename))
				}
				data, err := io.ReadAll(io.LimitReader(f, MaxFile+1))
				if err != nil || len(data) > MaxFile {
					clear(data)
					return problem("identity-read", filepath.Base(filename))
				}
				return add(data, "")
			}
			if st.Size() > 512<<20 {
				return problem("identity-zip", "")
			}
			z, err := zip.NewReader(f, st.Size())
			if err != nil || len(z.File) > maxZIPEntries {
				return problem("identity-zip", "")
			}
			for _, kind := range []string{"identity.pk8", "certificate.p7b"} {
				exact, fallback := []*zip.File{}, []*zip.File{}
				for _, entry := range z.File {
					if entry.FileInfo().IsDir() {
						continue
					}
					name := strings.ToLower(path.Base(entry.Name))
					if name == kind {
						exact = append(exact, entry)
					}
					if strings.HasSuffix(name, path.Ext(kind)) {
						fallback = append(fallback, entry)
					}
				}
				entries := exact
				if len(entries) == 0 {
					entries = fallback
				}
				if len(entries) > 1 {
					return problem("identity-duplicate", kind)
				}
				if len(entries) == 0 {
					continue
				}
				entry := entries[0]
				if entry.Flags&1 != 0 {
					return problem("identity-encrypted", kind)
				}
				if !entry.Mode().IsRegular() || entry.UncompressedSize64 > MaxFile {
					return problem("identity-read", kind)
				}
				r, err := entry.Open()
				if err != nil {
					return problem("identity-read", kind)
				}
				data, readErr := io.ReadAll(io.LimitReader(r, MaxFile+1))
				r.Close()
				if readErr != nil || len(data) > MaxFile {
					clear(data)
					return problem("identity-read", kind)
				}
				if err := add(data, kind); err != nil {
					return err
				}
			}
			return nil
		}()
		if err != nil {
			return pair, err
		}
	}
	if pair.Key == nil {
		return pair, problem("identity-missing", "identity.pk8")
	}
	if pair.Certificate == nil {
		return pair, problem("identity-missing", "certificate.p7b")
	}
	return pair, nil
}

func classify(data []byte) (string, []byte, error) {
	der := data
	label := ""
	if bytes.HasPrefix(bytes.TrimSpace(data), []byte("-----BEGIN")) {
		block, rest := pem.Decode(data)
		if block == nil || len(bytes.TrimSpace(rest)) != 0 {
			return "", nil, problem("identity-unrecognized", "")
		}
		defer clear(block.Bytes)
		label, der = block.Type, block.Bytes
		if label == "EC PRIVATE KEY" {
			return "", nil, problem("identity-key-format", "")
		}
		if len(block.Headers) != 0 {
			return "", nil, problem("identity-unrecognized", "")
		}
		if label != "PRIVATE KEY" && label != "PKCS7" && label != "CMS" {
			return "", nil, problem("identity-unrecognized", "")
		}
	}
	if label == "PRIVATE KEY" || label == "" {
		key, err := x509.ParsePKCS8PrivateKey(der)
		if err == nil {
			ec, ok := key.(*ecdsa.PrivateKey)
			if !ok {
				return "", nil, problem("identity-key-type", "")
			}
			defer ec.D.SetInt64(0)
			if ec.Curve != elliptic.P256() {
				return "", nil, problem("identity-key-curve", "")
			}
			return "identity.pk8", bytes.Clone(der), nil
		}
		if label == "PRIVATE KEY" {
			return "", nil, problem("identity-key", "")
		}
		if ec, err := x509.ParseECPrivateKey(der); err == nil {
			ec.D.SetInt64(0)
			return "", nil, problem("identity-key-format", "")
		}
	}
	if signedData(der) {
		return "certificate.p7b", bytes.Clone(der), nil
	}
	if label == "PKCS7" || label == "CMS" {
		return "", nil, problem("identity-certificate", "")
	}
	return "", nil, problem("identity-unrecognized", "")
}

// signedData checks the generic PKCS #7 / CMS envelope and SignedData structure, without
// inspecting certificates, identities, signatures, algorithms, or private-key relationships.
func signedData(data []byte) bool {
	var envelope struct {
		Type    asn1.ObjectIdentifier
		Content asn1.RawValue `asn1:"explicit,tag:0"`
	}
	rest, err := asn1.Unmarshal(data, &envelope)
	if err != nil || len(rest) != 0 || !envelope.Type.Equal(asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 2}) || envelope.Content.Class != 2 || envelope.Content.Tag != 0 || !envelope.Content.IsCompound {
		return false
	}
	var signed struct {
		Version int
		Digests []asn1.RawValue `asn1:"set"`
		Content struct {
			Type    asn1.ObjectIdentifier
			Content asn1.RawValue `asn1:"optional,explicit,tag:0"`
		}
		Certificates asn1.RawValue   `asn1:"optional,tag:0"`
		CRLs         asn1.RawValue   `asn1:"optional,tag:1"`
		Signers      []asn1.RawValue `asn1:"set"`
	}
	rest, err = asn1.Unmarshal(envelope.Content.Bytes, &signed)
	if err != nil || len(rest) != 0 || !validDER(data, 0) {
		return false
	}
	outer, ok := sequence(data)
	if !ok || len(outer) != 2 {
		return false
	}
	fields, ok := sequence(envelope.Content.Bytes)
	if !ok || len(fields) < 4 || len(fields) > 6 || signed.Version < 0 {
		return false
	}
	content, ok := sequence(fields[2].FullBytes)
	if !ok || len(content) < 1 || len(content) > 2 || len(content) == 2 && (content[1].Class != 2 || content[1].Tag != 0 || !content[1].IsCompound) {
		return false
	}
	i := 3
	for _, tag := range []int{0, 1} {
		if i < len(fields) && fields[i].Class == 2 && fields[i].Tag == tag && fields[i].IsCompound {
			i++
		}
	}
	if i != len(fields)-1 || fields[i].Class != 0 || fields[i].Tag != asn1.TagSet || !fields[i].IsCompound {
		return false
	}
	for _, digest := range signed.Digests {
		if !algorithm(digest) {
			return false
		}
	}
	for _, signer := range signed.Signers {
		if !signerInfo(signer) {
			return false
		}
	}
	return true
}

func sequence(data []byte) ([]asn1.RawValue, bool) {
	var seq asn1.RawValue
	rest, err := asn1.Unmarshal(data, &seq)
	if err != nil || len(rest) != 0 || seq.Class != 0 || seq.Tag != asn1.TagSequence || !seq.IsCompound {
		return nil, false
	}
	return elements(seq.Bytes)
}
func elements(data []byte) ([]asn1.RawValue, bool) {
	var out []asn1.RawValue
	for len(data) > 0 {
		var v asn1.RawValue
		rest, err := asn1.Unmarshal(data, &v)
		if err != nil || len(rest) >= len(data) {
			return nil, false
		}
		out = append(out, v)
		data = rest
	}
	return out, true
}
func algorithm(value asn1.RawValue) bool {
	f, ok := sequence(value.FullBytes)
	if !ok || len(f) < 1 || len(f) > 2 {
		return false
	}
	var oid asn1.ObjectIdentifier
	_, err := asn1.Unmarshal(f[0].FullBytes, &oid)
	return err == nil
}
func signerInfo(value asn1.RawValue) bool {
	f, ok := sequence(value.FullBytes)
	if !ok || len(f) < 5 {
		return false
	}
	var version int
	if _, err := asn1.Unmarshal(f[0].FullBytes, &version); err != nil || version < 0 {
		return false
	}
	if !(f[1].Class == 0 && f[1].Tag == asn1.TagSequence && f[1].IsCompound || f[1].Class == 2 && f[1].Tag == 0) || !algorithm(f[2]) {
		return false
	}
	i := 3
	if f[i].Class == 2 && f[i].Tag == 0 && f[i].IsCompound {
		i++
	}
	if i+1 >= len(f) || !algorithm(f[i]) || f[i+1].Class != 0 || f[i+1].Tag != asn1.TagOctetString || f[i+1].IsCompound {
		return false
	}
	i += 2
	if i < len(f) && f[i].Class == 2 && f[i].Tag == 1 && f[i].IsCompound {
		i++
	}
	return i == len(f)
}

func validDER(data []byte, depth int) bool {
	if depth > 32 {
		return false
	}
	for len(data) > 0 {
		var v asn1.RawValue
		rest, err := asn1.Unmarshal(data, &v)
		if err != nil || len(rest) >= len(data) {
			return false
		}
		if v.IsCompound && !validDER(v.Bytes, depth+1) {
			return false
		}
		data = rest
	}
	return true
}
