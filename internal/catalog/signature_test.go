// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package catalog

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func testSecret(t *testing.T) SecretKey {
	t.Helper()
	seed, err := hex.DecodeString("9d61b19deffd5a60ba844af492ec2cc44449c5697b326919703bac031cae7f60")
	if err != nil {
		t.Fatal(err)
	}
	return SecretKey{ID: [8]byte{1, 2, 3, 4, 5, 6, 7, 8}, Key: ed25519.NewKeyFromSeed(seed)}
}

func signedIndex(t *testing.T, i *Index) ([]byte, []byte) {
	t.Helper()
	b := indexBytes(t, i)
	s, err := Sign(b, testSecret(t), time.Unix(1792627200, 0))
	if err != nil {
		t.Fatal(err)
	}
	return b, s
}

func TestSignatureRoundTripAndFailures(t *testing.T) {
	b, sig := signedIndex(t, testIndex())
	if i, err := VerifyIndex(b, sig); err != nil || i.Sequence != 1 {
		t.Fatalf("%+v, %v", i, err)
	}
	if _, err := VerifyIndex(b, bytes.ReplaceAll(sig, []byte("\n"), []byte("\r\n"))); err != nil {
		t.Fatal(err)
	}
	packetChange := func(change func([]byte)) []byte {
		lines := strings.Split(string(sig), "\n")
		packet, _ := base64.StdEncoding.DecodeString(lines[1])
		change(packet)
		lines[1] = base64.StdEncoding.EncodeToString(packet)
		return []byte(strings.Join(lines, "\n"))
	}
	for _, tc := range []struct {
		name      string
		body, sig []byte
	}{
		{"body", bytes.Replace(b, []byte("openstick-v1"), []byte("openstick-v2"), 1), sig},
		{"comment", b, bytes.Replace(sig, []byte("timestamp:1792627200"), []byte("timestamp:1792627201"), 1)},
		{"key ID", b, packetChange(func(p []byte) { p[2]++ })},
		{"legacy Ed", b, packetChange(func(p []byte) { p[1] = 'd' })},
		{"body signature", b, packetChange(func(p []byte) { p[20]++ })},
		{"garbage base64", b, []byte("untrusted comment: x\n%%%\ntrusted comment: x\n%%%\n")},
		{"truncated packet", b, []byte("untrusted comment: x\nRUQ=\ntrusted comment: x\nAA==\n")},
		{"extra line", b, append(bytes.Clone(sig), '\n')},
		{"missing trusted prefix", b, bytes.Replace(sig, []byte("trusted comment: "), []byte("trusted: "), 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var invalid *SignatureError
			if _, err := VerifyIndex(tc.body, tc.sig); !errors.As(err, &invalid) {
				t.Fatalf("got %v, want SignatureError", err)
			}
		})
	}
	for _, comment := range []string{
		"timestamp:1792627200 file:index.json channel:stable sequence:1",
		"timestamp:1792627200 file:index.json channel:beta sequence:2",
		"timestamp:invalid file:index.json channel:beta sequence:1",
		"timestamp:1792627200 file:other.json channel:beta sequence:1",
	} {
		// Re-sign the global signature so this exercises the semantic binding, not just Ed25519.
		lines := strings.Split(string(sig), "\n")
		packet, _ := base64.StdEncoding.DecodeString(lines[1])
		lines[2] = "trusted comment: " + comment
		lines[3] = base64.StdEncoding.EncodeToString(ed25519.Sign(testSecret(t).Key, append(bytes.Clone(packet[10:]), []byte(comment)...)))
		var invalid *SignatureError
		if _, err := VerifyIndex(b, []byte(strings.Join(lines, "\n"))); !errors.As(err, &invalid) {
			t.Fatal(err)
		}
	}
	for _, pair := range [][2][]byte{{make([]byte, MaxIndexSize+1), sig}, {b, make([]byte, MaxSignatureSize+1)}} {
		if _, err := VerifyIndex(pair[0], pair[1]); !errors.Is(err, ErrTooLarge) {
			t.Fatal(err)
		}
	}
	// Independently alter the global signature; the file signature still verifies.
	lines := strings.Split(string(sig), "\n")
	glob, _ := base64.StdEncoding.DecodeString(lines[3])
	glob[0]++
	lines[3] = base64.StdEncoding.EncodeToString(glob)
	var invalid *SignatureError
	if _, err := VerifyIndex(b, []byte(strings.Join(lines, "\n"))); !errors.As(err, &invalid) {
		t.Fatal(err)
	}
}

func TestKeyFormatsAndRotation(t *testing.T) {
	pub, secret, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	p, err := ParsePublicKey(pub.Marshal())
	if err != nil {
		t.Fatal(err)
	}
	s, err := ParseSecretKey(secret.Marshal())
	if err != nil {
		t.Fatal(err)
	}
	b := indexBytes(t, testIndex())
	sig, err := Sign(b, s, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyIndexWithKeys(b, sig, []PublicKey{p}); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyIndex(b, sig); err == nil {
		t.Fatal("untrusted key accepted")
	}
	old := trustedPublicKeys
	trustedPublicKeys = append(append([]string(nil), old...), string(p.Marshal()))
	t.Cleanup(func() { trustedPublicKeys = old })
	if _, err := VerifyIndex(b, sig); err != nil {
		t.Fatal(err)
	}
	for _, data := range [][]byte{[]byte("bad"), []byte("untrusted comment: x\n%%%\n"), []byte("untrusted comment: x\nAA==\n")} {
		var invalid *SignatureError
		if _, err := ParsePublicKey(data); !errors.As(err, &invalid) {
			t.Fatal(err)
		}
		if _, err := ParseSecretKey(data); !errors.As(err, &invalid) {
			t.Fatal(err)
		}
	}
	secret.Key[63] ^= 1
	if _, err := ParseSecretKey(secret.Marshal()); err == nil {
		t.Fatal("inconsistent private key accepted")
	}
	if _, err := ParsePublicKey(make([]byte, MaxSignatureSize+1)); !errors.Is(err, ErrTooLarge) {
		t.Fatal(err)
	}
}

func TestBlake2b512(t *testing.T) {
	// RFC 7693 Appendix A's "abc" example and the BLAKE2 reference empty-message vector.
	for input, want := range map[string]string{
		"abc": "ba80a53f981c4d0d6a2797b69f12f6e94c212f14685ac4b74b12bb6fdbffa2d17d87c5392aab792dc252d5de4533cc9518d38aa8dbf1925ab92386edd4009923",
		"":    "786a02f742015903c6c6fd852552d272912f4740e15847618a86e217f71f5419d25e1031afee585313896444934eb04b903a685b1448b755d56f701afe9be2ce",
	} {
		if got := fmt.Sprintf("%x", blake2b512([]byte(input))); got != want {
			t.Fatalf("%q: %s", input, got)
		}
	}
	// Independent hashlib vectors cover block boundaries, counter increments and the index cap.
	for size, want := range map[int]string{
		127:          "b6292669ccd38d5f01caae96ba272c76a879a45743afa0725d83b9ebb26665b731f1848c52f11972b6644f554c064fa90780dbbbf3a89d4fc31f67df3e5857ef",
		128:          "2319e3789c47e2daa5fe807f61bec2a1a6537fa03f19ff32e87eecbfd64b7e0e8ccff439ac333b040f19b0c4ddd11a61e24ac1fe0f10a039806c5dcc0da3d115",
		129:          "f59711d44a031d5f97a9413c065d1e614c417ede998590325f49bad2fd444d3e4418be19aec4e11449ac1a57207898bc57d76a1bcf3566292c20c683a5c4648f",
		256:          "1ecc896f34d3f9cac484c73f75f6a5fb58ee6784be41b35f46067b9c65c63a6794d3d744112c653f73dd7deb6666204c5a9bfa5b46081fc10fdbe7884fa5cbf8",
		1024:         "6b490f42e902f61b1ee12d3c85e34152e37c94d07ab9ea577cad6a6eb4690fad38064f53a19c225703a5c52cdc9a85add71b339d327e1630ee3432b920240e8a",
		MaxIndexSize: "988b9f48e713f594ab9d5bbac2de5f1f04714169d0ff806844089dfed9d7dcad92d90e5c7d9d0676042e059ab231b6839113f2d2b8abf631ee0a49c593d4491d",
	} {
		input := make([]byte, size)
		for j := range input {
			input[j] = byte(j)
		}
		if got := fmt.Sprintf("%x", blake2b512(input)); got != want {
			t.Fatalf("%d bytes: %s", size, got)
		}
	}
}
