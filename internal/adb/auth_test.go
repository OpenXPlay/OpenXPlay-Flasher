// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package adb

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestKeyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "OpenXPlay Flasher", "adbkey")
	key, err := loadOrCreateKey(path)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if block, _ := pem.Decode(data); block == nil || block.Type != "PRIVATE KEY" {
		t.Fatalf("key file %q", data[:min(40, len(data))])
	}
	if runtime.GOOS != "windows" {
		if st, err := os.Stat(path); err != nil || st.Mode().Perm() != 0o600 {
			t.Fatalf("key file mode %v %v", st.Mode(), err)
		}
	}
	again, err := loadOrCreateKey(path)
	if err != nil || !again.Equal(key) {
		t.Fatalf("reload: %v", err)
	}
	// A PKCS#1 key, as older tools write it, is read too.
	pkcs1 := filepath.Join(t.TempDir(), "adbkey")
	os.WriteFile(pkcs1, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), 0o600)
	if k, err := loadOrCreateKey(pkcs1); err != nil || !k.Equal(key) {
		t.Fatalf("PKCS#1: %v", err)
	}
	small, _ := rsa.GenerateKey(rand.Reader, 1024)
	os.WriteFile(pkcs1, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(small)}), 0o600)
	if _, err := loadOrCreateKey(pkcs1); err == nil {
		t.Fatal("1024-bit key accepted")
	}
}

func testKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := loadOrCreateKey(filepath.Join(t.TempDir(), "adbkey"))
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func TestSignToken(t *testing.T) {
	key := testKey(t)
	token := []byte("0123456789abcdefghij")
	sig, err := signToken(key, token)
	if err != nil {
		t.Fatal(err)
	}
	// adbd verifies the token as a SHA-1 digest.
	if err := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA1, token, sig); err != nil {
		t.Fatal(err)
	}
	if len(sig) != 256 {
		t.Fatalf("signature of %d bytes", len(sig))
	}
	if _, err := signToken(key, token[:19]); err == nil {
		t.Fatal("short token signed")
	}
}

func TestAndroidPublicKey(t *testing.T) {
	key := testKey(t)
	enc, err := androidPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	text, ok := strings.CutSuffix(string(enc), " openxplay-flasher@host\x00")
	if !ok {
		t.Fatalf("suffix of %q", enc)
	}
	raw, err := base64.StdEncoding.DecodeString(text)
	if err != nil || len(raw) != 524 {
		t.Fatalf("struct of %d bytes: %v", len(raw), err)
	}
	le := binary.LittleEndian
	word := func(i int) uint32 { return le.Uint32(raw[4*i:]) }
	little := func(words []byte) *big.Int {
		be := make([]byte, len(words))
		for i := range words {
			be[len(words)-1-i] = words[i]
		}
		return new(big.Int).SetBytes(be)
	}
	n := key.PublicKey.N
	two32 := new(big.Int).Lsh(big.NewInt(1), 32)
	n0 := new(big.Int).Mod(n, two32)
	wantN0inv := new(big.Int).Sub(two32, new(big.Int).ModInverse(n0, two32))
	wantRR := new(big.Int).Exp(big.NewInt(2), big.NewInt(4096), n)
	switch {
	case word(0) != 64:
		t.Errorf("length %d words", word(0))
	case uint64(word(1)) != wantN0inv.Uint64():
		t.Errorf("n0inv %#x, want %#x", word(1), wantN0inv)
	case little(raw[8:264]).Cmp(n) != 0:
		t.Error("modulus differs")
	case little(raw[264:520]).Cmp(wantRR) != 0:
		t.Error("rr differs")
	case word(130) != 65537:
		t.Errorf("exponent %d", word(130))
	}
	if pub, err := decodeAndroidKey(enc); err != nil || !pub.Equal(&key.PublicKey) {
		t.Fatalf("decode: %v", err)
	}
	for _, n0 := range []uint32{1, 3, 0xffffffff, 0x8badf00d | 1} {
		if n0*n0inv(n0) != 0xffffffff {
			t.Errorf("n0inv(%#x)", n0)
		}
	}
}
