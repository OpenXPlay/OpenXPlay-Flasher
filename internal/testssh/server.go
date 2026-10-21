// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

// Package testssh is an in-process, loopback-only SSH fixture shared by integration tests.
package testssh

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"
)

type Handler func(command string, input io.Reader, stdout, stderr io.Writer) int

type Server struct {
	Listener    net.Listener
	Signer      ssh.Signer
	Password    string
	Key         ssh.PublicKey
	Handle      Handler
	mu          sync.Mutex
	connections map[*ssh.ServerConn]bool
	wg          sync.WaitGroup
}

func NewSigner(t testing.TB) (ssh.Signer, ed25519.PrivateKey) {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return signer, key
}
func New(t testing.TB, password string, key ssh.PublicKey, handle Handler) *Server {
	t.Helper()
	signer, _ := NewSigner(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{Listener: ln, Signer: signer, Password: password, Key: key, Handle: handle, connections: map[*ssh.ServerConn]bool{}}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			s.wg.Add(1)
			go s.serve(c)
		}
	}()
	t.Cleanup(func() {
		ln.Close()
		s.mu.Lock()
		for c := range s.connections {
			c.Close()
		}
		s.mu.Unlock()
		s.wg.Wait()
	})
	return s
}
func (s *Server) Fingerprint() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return ssh.FingerprintSHA256(s.Signer.PublicKey())
}
func (s *Server) HostPort() (string, int) {
	h, p, _ := net.SplitHostPort(s.Listener.Addr().String())
	n, _ := strconv.Atoi(p)
	return h, n
}
func (s *Server) ChangeKey(signer ssh.Signer) { s.mu.Lock(); s.Signer = signer; s.mu.Unlock() }
func (s *Server) serve(c net.Conn) {
	defer s.wg.Done()
	defer c.Close()
	config := &ssh.ServerConfig{PasswordCallback: func(meta ssh.ConnMetadata, p []byte) (*ssh.Permissions, error) {
		if s.Password != "" && string(p) == s.Password {
			return nil, nil
		}
		return nil, errors.New("authentication refused")
	}, PublicKeyCallback: func(meta ssh.ConnMetadata, k ssh.PublicKey) (*ssh.Permissions, error) {
		if s.Key != nil && string(k.Marshal()) == string(s.Key.Marshal()) {
			return nil, nil
		}
		return nil, errors.New("key refused")
	}}
	s.mu.Lock()
	config.AddHostKey(s.Signer)
	s.mu.Unlock()
	sc, channels, requests, err := ssh.NewServerConn(c, config)
	if err != nil {
		return
	}
	s.mu.Lock()
	s.connections[sc] = true
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.connections, sc); s.mu.Unlock(); sc.Close() }()
	go func() {
		for r := range requests {
			_ = r.Reply(r.Type == "keepalive@openxplay.org", nil)
		}
	}()
	var sessions sync.WaitGroup
	defer sessions.Wait()
	for incoming := range channels {
		if incoming.ChannelType() != "session" {
			_ = incoming.Reject(ssh.UnknownChannelType, "session only")
			continue
		}
		ch, reqs, err := incoming.Accept()
		if err != nil {
			continue
		}
		sessions.Add(1)
		go func() {
			defer sessions.Done()
			defer ch.Close()
			for r := range reqs {
				if r.Type != "exec" {
					_ = r.Reply(false, nil)
					continue
				}
				var exec struct{ Command string }
				if ssh.Unmarshal(r.Payload, &exec) != nil {
					_ = r.Reply(false, nil)
					return
				}
				_ = r.Reply(true, nil)
				code := s.Handle(exec.Command, ch, ch, ch.Stderr())
				_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Code uint32 }{uint32(code)}))
				return
			}
		}()
	}
}

// Unwrap extracts the shell command without executing any part of it on the host.
func Unwrap(command string) string {
	i := strings.Index(command, "sh -c '")
	if i < 0 {
		return command
	}
	value := strings.TrimPrefix(command[i:], "sh -c '")
	return strings.ReplaceAll(strings.TrimSuffix(value, "'"), `'\''`, "'")
}
func Result(out, errout io.Writer, stdout, stderr string, code int) int {
	fmt.Fprint(out, stdout)
	fmt.Fprint(errout, stderr)
	return code
}
