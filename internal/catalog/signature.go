// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package catalog

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// SignatureError is an invalid key, signature envelope, or signature verification result.
type SignatureError struct{ Reason string }

func (e *SignatureError) Error() string { return "catalog signature: " + e.Reason }

// PublicKey contains a minisign key ID and an Ed25519 public key.
type PublicKey struct {
	ID  [8]byte
	Key ed25519.PublicKey
}

// SecretKey is the maintainer's unencrypted key, never used by the downloader. Its file format
// is an untrusted comment line followed by base64(Ed || key ID || 64-byte Ed25519 private key).
// Store it with mode 0600, or pass it to the signing tool through standard input.
type SecretKey struct {
	ID  [8]byte
	Key ed25519.PrivateKey
}

func signatureLines(data []byte, count int) ([]string, error) {
	if len(data) > MaxSignatureSize {
		return nil, ErrTooLarge
	}
	s := strings.ReplaceAll(string(data), "\r\n", "\n")
	s = strings.TrimSuffix(s, "\n")
	lines := strings.Split(s, "\n")
	if len(lines) != count || !strings.HasPrefix(lines[0], "untrusted comment:") {
		return nil, &SignatureError{"invalid minisign envelope"}
	}
	return lines, nil
}

func keyBytes(data []byte, size int) ([]byte, error) {
	lines, err := signatureLines(data, 2)
	if err != nil {
		return nil, err
	}
	b, err := base64.StdEncoding.Strict().DecodeString(lines[1])
	if err != nil || len(b) != size || string(b[:2]) != "Ed" {
		return nil, &SignatureError{"invalid Ed25519 key encoding"}
	}
	return b, nil
}

// ParsePublicKey reads minisign's two-line public key format.
func ParsePublicKey(data []byte) (PublicKey, error) {
	b, err := keyBytes(data, 42)
	if err != nil {
		return PublicKey{}, err
	}
	var k PublicKey
	copy(k.ID[:], b[2:10])
	k.Key = ed25519.PublicKey(bytes.Clone(b[10:]))
	return k, nil
}

// ParseSecretKey reads our unencrypted secret format and checks its redundant public half.
func ParseSecretKey(data []byte) (SecretKey, error) {
	b, err := keyBytes(data, 74)
	if err != nil {
		return SecretKey{}, err
	}
	var k SecretKey
	copy(k.ID[:], b[2:10])
	k.Key = ed25519.PrivateKey(bytes.Clone(b[10:]))
	if !bytes.Equal(ed25519.NewKeyFromSeed(k.Key.Seed()), k.Key) {
		return SecretKey{}, &SignatureError{"inconsistent private key"}
	}
	return k, nil
}

// GenerateKey creates a random key ID and key pair for the maintainer tool.
func GenerateKey() (PublicKey, SecretKey, error) {
	pub, secret, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return PublicKey{}, SecretKey{}, err
	}
	var id [8]byte
	if _, err := rand.Read(id[:]); err != nil {
		return PublicKey{}, SecretKey{}, err
	}
	return PublicKey{id, pub}, SecretKey{id, secret}, nil
}

// Marshal returns a minisign public key file.
func (k PublicKey) Marshal() []byte {
	b := append([]byte("Ed"), k.ID[:]...)
	b = append(b, k.Key...)
	return []byte("untrusted comment: OpenXPlay catalog public key\n" + base64.StdEncoding.EncodeToString(b) + "\n")
}

// Marshal returns our unencrypted secret key file; the caller must protect its permissions.
func (k SecretKey) Marshal() []byte {
	b := append([]byte("Ed"), k.ID[:]...)
	b = append(b, k.Key...)
	return []byte("untrusted comment: OpenXPlay catalog secret key (unencrypted)\n" + base64.StdEncoding.EncodeToString(b) + "\n")
}

// Sign writes minisign's prehashed ED signature and global signature over the trusted comment.
// The input is validated first so the tool cannot accidentally publish an unusable catalog.
func Sign(data []byte, key SecretKey, now time.Time) ([]byte, error) {
	i, err := Decode(data)
	if err != nil {
		return nil, err
	}
	if len(key.Key) != ed25519.PrivateKeySize {
		return nil, &SignatureError{"invalid private key length"}
	}
	comment := fmt.Sprintf("timestamp:%d file:index.json channel:%s sequence:%d", now.Unix(), i.Channel, i.Sequence)
	hash := blake2b512(data)
	sig := ed25519.Sign(key.Key, hash[:])
	packet := append([]byte("ED"), key.ID[:]...)
	packet = append(packet, sig...)
	global := ed25519.Sign(key.Key, append(bytes.Clone(sig), []byte(comment)...))
	return []byte("untrusted comment: signature from OpenXPlay catalog tool\n" + base64.StdEncoding.EncodeToString(packet) + "\ntrusted comment: " + comment + "\n" + base64.StdEncoding.EncodeToString(global) + "\n"), nil
}

// VerifyIndex checks both signatures against only the keys embedded in keys.go, then decodes
// the index and binds its channel and sequence to the authenticated trusted comment.
func VerifyIndex(data, signature []byte) (*Index, error) {
	var keys []PublicKey
	for _, text := range trustedPublicKeys {
		k, err := ParsePublicKey([]byte(text))
		if err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return VerifyIndexWithKeys(data, signature, keys)
}

// VerifyIndexWithKeys is for the maintainer tool's explicit --public check. Source never accepts
// caller-supplied keys; changing a download URL cannot change the client's trust roots.
func VerifyIndexWithKeys(data, signature []byte, keys []PublicKey) (*Index, error) {
	if len(data) > MaxIndexSize || len(signature) > MaxSignatureSize {
		return nil, ErrTooLarge
	}
	lines, err := signatureLines(signature, 4)
	if err != nil {
		return nil, err
	}
	packet, err := base64.StdEncoding.Strict().DecodeString(lines[1])
	if err != nil || len(packet) != 74 {
		return nil, &SignatureError{"invalid signature encoding"}
	}
	if string(packet[:2]) != "ED" {
		return nil, &SignatureError{"only prehashed ED signatures are accepted"}
	}
	if !strings.HasPrefix(lines[2], "trusted comment: ") {
		return nil, &SignatureError{"missing trusted comment"}
	}
	comment := strings.TrimPrefix(lines[2], "trusted comment: ")
	global, err := base64.StdEncoding.Strict().DecodeString(lines[3])
	if err != nil || len(global) != ed25519.SignatureSize {
		return nil, &SignatureError{"invalid global signature encoding"}
	}
	hash := blake2b512(data)
	verified := false
	for _, key := range keys {
		if len(key.Key) != ed25519.PublicKeySize || !bytes.Equal(packet[2:10], key.ID[:]) {
			continue
		}
		if ed25519.Verify(key.Key, hash[:], packet[10:]) && ed25519.Verify(key.Key, append(bytes.Clone(packet[10:]), []byte(comment)...), global) {
			verified = true
			break
		}
	}
	if !verified {
		return nil, &SignatureError{"unknown key ID or invalid signature"}
	}
	i, err := Decode(data)
	if err != nil {
		return nil, err
	}
	fields := strings.Fields(comment)
	if len(fields) != 4 || !strings.HasPrefix(fields[0], "timestamp:") || fields[1] != "file:index.json" || fields[2] != "channel:"+i.Channel || fields[3] != "sequence:"+strconv.FormatUint(i.Sequence, 10) {
		return nil, &SignatureError{"trusted comment channel/sequence does not match the index"}
	}
	if _, err := strconv.ParseInt(strings.TrimPrefix(fields[0], "timestamp:"), 10, 64); err != nil {
		return nil, &SignatureError{"invalid trusted timestamp"}
	}
	return i, nil
}
