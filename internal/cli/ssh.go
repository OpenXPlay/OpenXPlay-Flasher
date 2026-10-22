// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/user"
	"strconv"
	"strings"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/app"
	sshdev "github.com/OpenXPlay/OpenXPlay-Flasher/internal/ssh"
)

type sshFlags struct {
	target, key, accept string
}

func (f *sshFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&f.target, "ssh", "", "[user@]host[:port] / SSH 登录地址")
	fs.StringVar(&f.key, "ssh-key", "", "SSH private key file / SSH 私钥文件")
	fs.StringVar(&f.accept, "accept-host-key", "", "accept this SHA256 fingerprint once / 接受此 SHA256 主机密钥指纹")
}
func (f sshFlags) enabled() bool { return f.target != "" }
func parseSSHTarget(target string) (host, userName string, port int, err error) {
	port = 22
	if u, ok := strings.LastIndex(target, "@"), strings.Contains(target, "@"); ok {
		userName, target = target[:u], target[u+1:]
	}
	if strings.HasPrefix(target, "[") && strings.HasSuffix(target, "]") {
		host = strings.TrimSuffix(strings.TrimPrefix(target, "["), "]")
	} else if strings.Count(target, ":") == 1 || strings.HasPrefix(target, "[") {
		var p string
		host, p, err = net.SplitHostPort(target)
		if err == nil {
			port, err = strconv.Atoi(p)
		}
	} else {
		host = target
	}
	if err != nil {
		return
	}
	if port < 1 || port > 65535 {
		err = errors.New("Invalid SSH port / SSH 端口无效")
		return
	}
	_, err = sshdev.HostPort(host, port)
	return
}

// readSecret is injectable so CLI tests never read a real terminal.
var readSecret = terminalSecret

func (f sshFlags) connect(ctx context.Context, svc *app.Service, stdin io.Reader, stderr io.Writer) error {
	if !f.enabled() {
		if f.key != "" || f.accept != "" {
			return errors.New("Use --ssh / 请使用 --ssh")
		}
		return nil
	}
	host, userName, port, err := parseSSHTarget(f.target)
	if err != nil {
		return err
	}
	if userName == "" {
		current, e := user.Current()
		if e != nil {
			return e
		}
		userName = current.Username
		if i := strings.LastIndex(userName, `\`); i >= 0 {
			userName = userName[i+1:]
		}
	}
	password := os.Getenv("OXP_FLASHER_SSH_PASSWORD")
	if password == "" && f.key == "" {
		password, err = readSecret(stdin, stderr, "SSH password / SSH 密码: ")
		if err != nil {
			return err
		}
	}
	opts := sshdev.Options{Host: host, Port: port, User: userName, Password: password, KeyFile: f.key, AcceptHostKey: f.accept}
	_, err = svc.SSHConnect(ctx, opts)
	var pass *sshdev.KeyPassphraseError
	if errors.As(err, &pass) {
		opts.Passphrase, err = readSecret(stdin, stderr, "Private key passphrase / 私钥口令: ")
		if err == nil {
			_, err = svc.SSHConnect(ctx, opts)
		}
	}
	var hostKey *sshdev.HostKeyError
	if errors.As(err, &hostKey) && !hostKey.Changed {
		fmt.Fprintln(stderr, "Verify and retry with / 核对后使用此参数重试: --accept-host-key", hostKey.Fingerprint)
	}
	return err
}
