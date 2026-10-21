// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

// Package identity performs generic DER syntax checks only. It never persists, logs, or
// interprets accessory credentials. Callers own and discard the returned in-memory bytes.
package identity

import (
	"archive/zip"
	"crypto/x509"
	"encoding/asn1"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/stage"
)

const MaxFile = 4 << 20

type Pair struct{ Key, Certificate []byte }

func problem(code, name string) error { return &stage.Problem{Code: code, Detail: name} }

// Read accepts the two named files in any order, or a ZIP with those basenames anywhere
// inside it. ZIP entries are read directly; extraction never touches the filesystem.
func Read(paths []string) (Pair, error) {
	files := map[string][]byte{}
	add := func(name string, r io.Reader) error {
		if name != "identity.pk8" && name != "certificate.p7b" {
			return nil
		}
		if _, exists := files[name]; exists {
			return problem("identity-duplicate", name)
		}
		data, err := io.ReadAll(io.LimitReader(r, MaxFile+1))
		if err != nil || len(data) > MaxFile {
			return problem("identity-read", name)
		}
		files[name] = data
		return nil
	}
	for _, filename := range paths {
		st, err := os.Stat(filename)
		if err != nil || !st.Mode().IsRegular() {
			return Pair{}, problem("identity-read", filepath.Base(filename))
		}
		if strings.EqualFold(filepath.Ext(filename), ".zip") {
			z, err := zip.OpenReader(filename)
			if err != nil {
				return Pair{}, problem("identity-zip", "")
			}
			var readErr error
			for _, entry := range z.File {
				name := path.Base(entry.Name)
				if entry.FileInfo().IsDir() || name != "identity.pk8" && name != "certificate.p7b" {
					continue
				}
				if entry.UncompressedSize64 > MaxFile || !entry.Mode().IsRegular() {
					readErr = problem("identity-read", name)
					break
				}
				r, err := entry.Open()
				if err != nil {
					readErr = problem("identity-read", name)
					break
				}
				readErr = add(name, r)
				r.Close()
				if readErr != nil {
					break
				}
			}
			z.Close()
			if readErr != nil {
				return Pair{}, readErr
			}
		} else {
			name := filepath.Base(filename)
			if name != "identity.pk8" && name != "certificate.p7b" {
				return Pair{}, problem("identity-name", "")
			}
			f, err := os.Open(filename)
			if err != nil {
				return Pair{}, problem("identity-read", name)
			}
			st, err := f.Stat()
			if err == nil && st.Mode().IsRegular() && st.Size() <= MaxFile {
				err = add(name, f)
			} else {
				err = problem("identity-read", name)
			}
			f.Close()
			if err != nil {
				return Pair{}, err
			}
		}
	}
	for _, name := range []string{"identity.pk8", "certificate.p7b"} {
		if _, ok := files[name]; !ok {
			return Pair{}, problem("identity-missing", name)
		}
	}
	pair := Pair{Key: files["identity.pk8"], Certificate: files["certificate.p7b"]}
	if _, err := x509.ParsePKCS8PrivateKey(pair.Key); err != nil {
		return Pair{}, problem("identity-key", "identity.pk8")
	}
	if !signedData(pair.Certificate) {
		return Pair{}, problem("identity-certificate", "certificate.p7b")
	}
	return pair, nil
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
