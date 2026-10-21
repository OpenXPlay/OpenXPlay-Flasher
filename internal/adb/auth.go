// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package adb

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"math/big"
	"os"
	"path/filepath"
)

// adbd authenticates a host by a 20-byte token the host signs with its RSA-2048 key. A device
// that does not know the key gets the public key in Android's format and asks its user.
const (
	keyBits    = 2048
	keyWords   = keyBits / 32
	keyComment = " openxplay-flasher@host\x00"
)

// defaultKeyPath is <user config dir>/OpenXPlay Flasher/adbkey.
func defaultKeyPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "OpenXPlay Flasher", "adbkey"), nil
}

// loadOrCreateKey reads the PEM private key at path, creating it (mode 0600) when missing.
func loadOrCreateKey(path string) (*rsa.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		return parseKey(data)
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	key, err := rsa.GenerateKey(rand.Reader, keyBits)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		// Another process created it first.
		return loadOrCreateKey(path)
	}
	if err != nil {
		return nil, err
	}
	err = pem.Encode(f, &pem.Block{Type: "PRIVATE KEY", Bytes: der})
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(path)
		return nil, err
	}
	return key, nil
}

// parseKey accepts PKCS#8 ("PRIVATE KEY") and PKCS#1 ("RSA PRIVATE KEY") RSA-2048 keys.
func parseKey(data []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("adb: the key file holds no PEM block")
	}
	var key *rsa.PrivateKey
	switch block.Type {
	case "PRIVATE KEY":
		k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, err
		}
		var ok bool
		if key, ok = k.(*rsa.PrivateKey); !ok {
			return nil, errors.New("adb: the key is not an RSA key")
		}
	case "RSA PRIVATE KEY":
		k, err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return nil, err
		}
		key = k
	default:
		return nil, fmt.Errorf("adb: unsupported key type %q", block.Type)
	}
	if key.N.BitLen() != keyBits {
		return nil, fmt.Errorf("adb: the key has %d bits, adb needs %d", key.N.BitLen(), keyBits)
	}
	return key, nil
}

// signToken signs the token as adbd checks it: as if it were a SHA-1 digest (PKCS#1 v1.5 with the
// SHA-1 DigestInfo prefix).
func signToken(key *rsa.PrivateKey, token []byte) ([]byte, error) {
	if len(token) != sha1.Size {
		return nil, fmt.Errorf("adb: auth token of %d bytes, want %d", len(token), sha1.Size)
	}
	return rsa.SignPKCS1v15(nil, key, crypto.SHA1, token)
}

// androidPublicKey encodes pub as adbd stores keys: base64 of the little-endian struct {words,
// n0inv, n[words], rr[words], exponent} (n and rr least significant word first, n0inv = -1/n mod
// 2^32, rr = 2^(2*bits) mod n), then a space, a comment and a NUL.
func androidPublicKey(pub *rsa.PublicKey) ([]byte, error) {
	if pub.N.BitLen() != keyBits {
		return nil, fmt.Errorf("adb: the key has %d bits, adb needs %d", pub.N.BitLen(), keyBits)
	}
	le := binary.LittleEndian
	raw := make([]byte, 4*(3+2*keyWords))
	le.PutUint32(raw[0:], keyWords)
	le.PutUint32(raw[4:], n0inv(uint32(pub.N.Uint64())))
	putLittle(raw[8:8+4*keyWords], pub.N)
	rr := new(big.Int).Lsh(big.NewInt(1), 2*keyBits)
	putLittle(raw[8+4*keyWords:8+8*keyWords], rr.Mod(rr, pub.N))
	le.PutUint32(raw[8+8*keyWords:], uint32(pub.E))
	out := base64.StdEncoding.AppendEncode(nil, raw)
	return append(out, keyComment...), nil
}

// n0inv returns -1/n0 mod 2^32 for an odd n0; each Newton step doubles the correct low bits.
func n0inv(n0 uint32) uint32 {
	x := n0 // correct to 3 bits for odd n0
	for range 4 {
		x *= 2 - n0*x
	}
	return -x
}

// putLittle writes x into dst as a little-endian number of len(dst) bytes.
func putLittle(dst []byte, x *big.Int) {
	x.FillBytes(dst)
	for i, j := 0, len(dst)-1; i < j; i, j = i+1, j-1 {
		dst[i], dst[j] = dst[j], dst[i]
	}
}
