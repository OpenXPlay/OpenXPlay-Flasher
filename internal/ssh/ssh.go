// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package ssh

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/adb"
	cryptossh "golang.org/x/crypto/ssh"
)

// Dialer is the TCP boundary; fixtures can substitute an in-process connection.
type Dialer interface {
	DialContext(context.Context, string, string) (net.Conn, error)
}

// Options intentionally has no serializable credential fields. Never include it in state,
// settings, diagnostics or formatted errors.
type Options struct {
	Dialer            Dialer
	Host              string
	Port              int
	User              string
	Password          string `json:"-"`
	KeyFile           string
	Passphrase        string `json:"-"`
	KnownHosts        string
	AcceptHostKey     string
	DialTimeout       time.Duration
	CommandTimeout    time.Duration
	KeepaliveInterval time.Duration
}

// String and GoString make accidental diagnostic formatting safe as well as JSON encoding.
func (Options) String() string     { return "SSH connection options (credentials omitted)" }
func (o Options) GoString() string { return o.String() }

type KeyPassphraseError struct{}

// ConnectionError classifies failures without putting credentials or transport diagnostics in
// user-facing messages. Unwrap preserves cancellation and network error checks for callers.
type ConnectionError struct {
	Kind string
	Err  error
}

func (e *ConnectionError) Error() string {
	switch e.Kind {
	case "refused":
		return "SSH connection refused; no SSH server on this port / SSH 连接被拒绝，此端口没有 SSH 服务"
	case "timeout", "no-greeting":
		return "SSH timed out or sent no greeting; check the address, power and direct routing in proxy/VPN apps / SSH 连接超时或未收到 SSH 握手信息，请检查地址、电源，并在代理或 VPN 应用中将此地址设为直连"
	case "authentication":
		return "SSH authentication failed; check the user name, password or private key / SSH 认证失败，请检查用户名、密码或私钥"
	default:
		return "SSH connection failed / SSH 连接失败"
	}
}
func (e *ConnectionError) Unwrap() error { return e.Err }

func connectionError(err error) error {
	var host *HostKeyError
	if errors.As(err, &host) || errors.Is(err, context.Canceled) {
		return err
	}
	kind := "connection"
	var network net.Error
	switch {
	case errors.Is(err, syscall.ECONNREFUSED):
		kind = "refused"
	case errors.Is(err, context.DeadlineExceeded) || errors.As(err, &network) && network.Timeout():
		kind = "timeout"
	case strings.Contains(err.Error(), "unable to authenticate"):
		kind = "authentication"
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		kind = "no-greeting"
	}
	return &ConnectionError{Kind: kind, Err: err}
}

func (*KeyPassphraseError) Error() string {
	return "The SSH private key needs a passphrase / SSH 私钥需要口令"
}

type SudoError struct{ Kind string }

func (e *SudoError) Error() string {
	switch e.Kind {
	case "password-required":
		return "sudo needs the login password; enter it to continue / sudo 需要登录密码，请输入后重试"
	case "incorrect-password":
		return "sudo rejected the login password / sudo 拒绝了登录密码"
	default:
		return "The login user is not allowed to use sudo / 此登录用户没有 sudo 权限"
	}
}

// Connector retains parsed authentication material only in memory and opens a fresh transport
// on each call, including each attempt of the stage executor's boot-id wait.
type Connector struct {
	opts Options
	auth []cryptossh.AuthMethod
}

// HostPort validates a form address without using authentication material.
func HostPort(host string, port int) (string, error) {
	host = strings.TrimSpace(host)
	if port == 0 {
		port = 22
	}
	if host == "" || strings.ContainsAny(host, "\x00\r\n /@") || port < 1 || port > 65535 {
		return "", errors.New("Invalid SSH host or port / SSH 主机或端口无效")
	}
	return net.JoinHostPort(host, strconv.Itoa(port)), nil
}
func NewConnector(o Options) (*Connector, error) {
	if o.Port == 0 {
		o.Port = 22
	}
	o.Host = strings.TrimSpace(o.Host)
	if o.Host == "" || strings.ContainsAny(o.Host, "\x00\r\n /@") || o.Port < 1 || o.Port > 65535 || o.User == "" || strings.ContainsAny(o.User, "\x00\r\n") || o.KnownHosts == "" {
		return nil, errors.New("Invalid SSH host, port or user / SSH 主机、端口或用户名无效")
	}
	if strings.ContainsAny(o.Password, "\r\n") {
		return nil, errors.New("The sudo password cannot contain a newline / sudo 密码不能包含换行符")
	}
	if o.DialTimeout <= 0 {
		o.DialTimeout = 10 * time.Second
	}
	if o.CommandTimeout <= 0 {
		o.CommandTimeout = 10 * time.Minute
	}
	if o.KeepaliveInterval <= 0 {
		o.KeepaliveInterval = 15 * time.Second
	}
	if o.Dialer == nil {
		o.Dialer = &net.Dialer{}
	}
	c := &Connector{opts: o}
	if o.KeyFile != "" {
		data, err := os.ReadFile(o.KeyFile)
		if err != nil {
			return nil, err
		}
		defer clear(data)
		var signer cryptossh.Signer
		signer, err = cryptossh.ParsePrivateKey(data)
		var encrypted *cryptossh.PassphraseMissingError
		if errors.As(err, &encrypted) {
			if o.Passphrase == "" {
				return nil, &KeyPassphraseError{}
			}
			pass := []byte(o.Passphrase)
			signer, err = cryptossh.ParsePrivateKeyWithPassphrase(data, pass)
			clear(pass)
		}
		if err != nil {
			return nil, errors.New("Cannot unlock the SSH private key / 无法解锁 SSH 私钥")
		}
		c.auth = append(c.auth, cryptossh.PublicKeys(signer))
	}
	c.opts.Passphrase = ""
	if o.Password != "" {
		c.auth = append(c.auth, cryptossh.Password(o.Password))
	}
	if len(c.auth) == 0 {
		return nil, errors.New("Enter a password or choose a private key / 请输入密码或选择私钥")
	}
	return c, nil
}
func (c *Connector) Address() string { return net.JoinHostPort(c.opts.Host, strconv.Itoa(c.opts.Port)) }
func (c *Connector) Connect(ctx context.Context) (adb.Device, error) {
	ctx, cancel := context.WithTimeout(ctx, c.opts.DialTimeout)
	defer cancel()
	conn, err := c.opts.Dialer.DialContext(ctx, "tcp", c.Address())
	if err != nil {
		return nil, connectionError(err)
	}
	deadline, _ := ctx.Deadline()
	_ = conn.SetDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	sc, ch, req, err := cryptossh.NewClientConn(conn, c.Address(), &cryptossh.ClientConfig{User: c.opts.User, Auth: c.auth, HostKeyCallback: hostCallback(c.opts.KnownHosts, c.opts.AcceptHostKey)})
	if !stop() || ctx.Err() != nil {
		conn.Close()
		if ctx.Err() != nil {
			return nil, connectionError(ctx.Err())
		}
	}
	if err != nil {
		conn.Close()
		return nil, connectionError(err)
	}
	_ = conn.SetDeadline(time.Time{})
	d := &Device{client: cryptossh.NewClient(sc, ch, req), opts: c.opts, done: make(chan struct{})}
	go d.keepalive()
	return d, nil
}

type Device struct {
	client   *cryptossh.Client
	opts     Options
	once     sync.Once
	done     chan struct{}
	sudoMu   sync.Mutex
	sudoMode string
}

var _ adb.Device = (*Device)(nil)

func (d *Device) Close() error {
	var err error
	d.once.Do(func() { close(d.done); err = d.client.Close() })
	return err
}
func (d *Device) keepalive() {
	t := time.NewTicker(d.opts.KeepaliveInterval)
	defer t.Stop()
	for {
		select {
		case <-d.done:
			return
		case <-t.C:
			replied := make(chan struct{})
			go func() {
				defer close(replied)
				_, _, err := d.client.SendRequest("keepalive@openxplay.org", true, nil)
				if err != nil {
					d.Close()
				}
			}()
			timer := time.NewTimer(d.opts.DialTimeout)
			select {
			case <-d.done:
				timer.Stop()
				return
			case <-replied:
				timer.Stop()
			case <-timer.C:
				d.Close()
				return
			}
		}
	}
}
func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// boundedBuffer drains excess output so a remote process cannot grow the application's memory.
type boundedBuffer struct {
	bytes.Buffer
	max      int64
	tooLarge bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	left := max(0, b.max-int64(b.Len()))
	if int64(n) > left {
		b.tooLarge = true
		p = p[:left]
	}
	_, _ = b.Buffer.Write(p)
	return n, nil
}
func (d *Device) raw(ctx context.Context, cmd string, input io.Reader, maxOut int64) (adb.Result, error) {
	ctx, cancel := context.WithTimeout(ctx, d.opts.CommandTimeout)
	defer cancel()
	// Closing the client on cancellation also interrupts a stalled channel-open request.
	stop := context.AfterFunc(ctx, func() { d.Close() })
	defer stop()
	session, err := d.client.NewSession()
	if err != nil {
		if ctx.Err() != nil {
			return adb.Result{}, ctx.Err()
		}
		return adb.Result{}, err
	}
	defer session.Close()
	out, stderr := &boundedBuffer{max: maxOut}, &boundedBuffer{max: 4 << 20}
	session.Stdout, session.Stderr, session.Stdin = out, stderr, input
	err = session.Run(cmd)
	result := adb.Result{Stdout: out.Bytes(), Stderr: stderr.Bytes()}
	if ctx.Err() != nil {
		return adb.Result{}, ctx.Err()
	}
	var status *cryptossh.ExitError
	if errors.As(err, &status) {
		result.ExitCode = status.ExitStatus()
		err = nil
	}
	if err == nil && (out.tooLarge || stderr.tooLarge) {
		return adb.Result{}, adb.ErrTooLarge
	}
	return result, err
}
func sudoFailure(result adb.Result) error {
	if result.ExitCode == 0 {
		return nil
	}
	s := strings.ToLower(string(result.Stderr))
	switch {
	case strings.Contains(s, "not in the sudoers"), strings.Contains(s, "not allowed to execute"), strings.Contains(s, "may not run sudo"), strings.Contains(s, "not allowed to run sudo"), strings.Contains(s, "not in sudoers"):
		return &SudoError{"not-sudoer"}
	case strings.Contains(s, "incorrect password"), strings.Contains(s, "sorry, try again"):
		return &SudoError{"incorrect-password"}
	case strings.Contains(s, "password is required"), strings.Contains(s, "no password was provided"), strings.Contains(s, "a terminal is required"):
		return &SudoError{"password-required"}
	}
	return nil
}
func (d *Device) root(ctx context.Context, cmd string, input io.Reader, maxOut int64) (adb.Result, error) {
	if d.opts.User == "root" {
		return d.raw(ctx, "sh -c "+quote(cmd), input, maxOut)
	}
	d.sudoMu.Lock()
	if d.sudoMode == "" {
		result, err := d.raw(ctx, "LC_ALL=C sudo -n -p '' -- true", nil, 4096)
		if err != nil {
			d.sudoMu.Unlock()
			return adb.Result{}, err
		}
		if result.ExitCode == 0 {
			d.sudoMode = "nopass"
		} else {
			failure := sudoFailure(result)
			var se *SudoError
			if errors.As(failure, &se) && se.Kind == "not-sudoer" {
				d.sudoMu.Unlock()
				return adb.Result{}, failure
			}
			if d.opts.Password == "" {
				d.sudoMu.Unlock()
				return adb.Result{}, &SudoError{"password-required"}
			}
			// sudo -S reads further stdin lines as new attempts after a wrong password, so check the
			// password once with nothing behind it before any command's input follows it.
			check, err := d.raw(ctx, "LC_ALL=C sudo -k -S -p '' -- true", strings.NewReader(d.opts.Password+"\n"), 4096)
			if err == nil && check.ExitCode != 0 {
				if err = sudoFailure(check); err == nil {
					err = &SudoError{"incorrect-password"}
				}
			}
			if err != nil {
				d.sudoMu.Unlock()
				return adb.Result{}, err
			}
			d.sudoMode = "password"
		}
	}
	mode := d.sudoMode
	d.sudoMu.Unlock()
	prefix := "LC_ALL=C sudo -n -p '' -- "
	if mode == "password" {
		prefix = "LC_ALL=C sudo -k -S -p '' -- "
		if input == nil {
			input = strings.NewReader("")
		}
		input = io.MultiReader(strings.NewReader(d.opts.Password+"\n"), input)
	}
	result, err := d.raw(ctx, prefix+"sh -c "+quote(cmd), input, maxOut)
	if err == nil {
		err = sudoFailure(result)
	}
	return result, err
}
func (d *Device) Shell(ctx context.Context, cmd string) (adb.Result, error) {
	if strings.TrimSpace(cmd) == "" {
		return adb.Result{}, errors.New("Empty SSH command / SSH 命令为空")
	}
	r, err := d.root(ctx, cmd, nil, 64<<20)
	// Command output can enter existing stage logs; credentials must never reach those logs,
	// even when a failing remote sudo implementation echoes its input.
	if d.opts.Password != "" {
		r.Stdout = bytes.ReplaceAll(r.Stdout, []byte(d.opts.Password), []byte("[redacted]"))
		r.Stderr = bytes.ReplaceAll(r.Stderr, []byte(d.opts.Password), []byte("[redacted]"))
	}
	return r, err
}
func validPath(p string) bool {
	return path.IsAbs(p) && !strings.ContainsAny(p, "\x00\n\r") && path.Clean(p) != "/"
}
func (d *Device) Push(ctx context.Context, p string, data []byte, mode fs.FileMode) error {
	if !validPath(p) {
		return errors.New("Invalid remote file path / 远程文件路径无效")
	}
	cmd := `set -eu
umask 077
target=` + quote(p) + `
tmp=$(mktemp -- "${target}.oxpf-XXXXXXXX")
trap 'rm -f -- "$tmp"' EXIT HUP INT TERM
cat > "$tmp"
chmod ` + fmt.Sprintf("%o", mode.Perm()) + ` "$tmp"
mv -f -- "$tmp" "$target"`
	r, err := d.root(ctx, cmd, bytes.NewReader(data), 4096)
	if err == nil && r.ExitCode != 0 {
		err = fmt.Errorf("SSH upload failed (exit %d) / SSH 上传失败（退出码 %d）", r.ExitCode, r.ExitCode)
	}
	return err
}
func (d *Device) Pull(ctx context.Context, p string, limit int64) ([]byte, error) {
	if !validPath(p) || limit < 0 || limit == 1<<63-1 {
		return nil, errors.New("Invalid remote file path or limit / 远程文件路径或大小限制无效")
	}
	// Use one open descriptor for stat and read. head bounds a file that grows after the check.
	cmd := `set -eu
exec 3< ` + quote(p) + `
size=$(stat -Lc '%s' /proc/self/fd/3)
[ "$size" -le ` + strconv.FormatInt(limit, 10) + ` ] || exit 73
head -c ` + strconv.FormatInt(limit+1, 10) + ` <&3`
	r, err := d.root(ctx, cmd, nil, limit+1)
	if err != nil {
		return nil, err
	}
	if r.ExitCode == 73 || int64(len(r.Stdout)) > limit {
		return nil, adb.ErrTooLarge
	}
	if r.ExitCode != 0 {
		return nil, fmt.Errorf("SSH download failed (exit %d) / SSH 下载失败（退出码 %d）", r.ExitCode, r.ExitCode)
	}
	return r.Stdout, nil
}
