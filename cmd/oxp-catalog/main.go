// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

// oxp-catalog builds and signs release indexes on a maintainer's machine. It does not access
// devices or share the application's configuration, and downloads only with build --fetch.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/catalog"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdin, os.Stdout, catalog.NewHTTPClient()); err != nil {
		fmt.Fprintln(os.Stderr, "oxp-catalog:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, in io.Reader, out io.Writer, client *http.Client) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(args) == 0 {
		return fmt.Errorf("expected keygen, build, sign or verify")
	}
	f := flag.NewFlagSet(args[0], flag.ContinueOnError)
	f.SetOutput(out)
	switch args[0] {
	case "keygen":
		public := f.String("public", "", "public key file (must not exist)")
		secret := f.String("secret", "", "unencrypted secret key file (must not exist)")
		if err := parse(f, args[1:]); err != nil {
			return err
		}
		if *public == "" || *secret == "" {
			return fmt.Errorf("keygen requires --public and --secret")
		}
		pub, key, err := catalog.GenerateKey()
		if err != nil {
			return err
		}
		if err := createKey(ctx, *public, pub.Marshal(), 0o644); err != nil {
			return err
		}
		if err := createKey(ctx, *secret, key.Marshal(), 0o600); err != nil {
			os.Remove(*public)
			return err
		}
		fmt.Fprintln(out, "created", *public, "and", *secret)
		return nil
	case "build":
		source := f.String("source", "", "source JSON")
		dir := f.String("out", "", "output directory")
		previous := f.String("previous", "", "previous index; sequence must increase")
		fetch := f.Bool("fetch", false, "download artifacts missing local files and hashes")
		if err := parse(f, args[1:]); err != nil {
			return err
		}
		if *source == "" || *dir == "" {
			return fmt.Errorf("build requires --source and --out")
		}
		i, err := build(ctx, *source, *previous, *fetch, client, time.Now())
		if err != nil {
			return err
		}
		data, err := encodeIndex(i)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(*dir, 0o755); err != nil {
			return err
		}
		path := filepath.Join(*dir, "index.json")
		if err := catalog.WriteFile(ctx, path, data, 0o644); err != nil {
			return err
		}
		fmt.Fprintln(out, path)
		return nil
	case "sign":
		secret := f.String("secret", "", "secret key file, or - for standard input")
		index := f.String("index", "", "index.json to sign")
		if err := parse(f, args[1:]); err != nil {
			return err
		}
		if *secret == "" || *index == "" {
			return fmt.Errorf("sign requires --secret and --index")
		}
		var keyData []byte
		var err error
		if *secret == "-" {
			keyData, err = io.ReadAll(io.LimitReader(in, catalog.MaxSignatureSize+1))
		} else {
			keyData, err = catalog.ReadFile(ctx, *secret, catalog.MaxSignatureSize)
		}
		if err != nil {
			return err
		}
		key, err := catalog.ParseSecretKey(keyData)
		if err != nil {
			return err
		}
		data, err := catalog.ReadFile(ctx, *index, catalog.MaxIndexSize)
		if err != nil {
			return err
		}
		sig, err := catalog.Sign(data, key, time.Now())
		if err != nil {
			return err
		}
		if err := catalog.WriteFile(ctx, *index+".minisig", sig, 0o644); err != nil {
			return err
		}
		fmt.Fprintln(out, *index+".minisig")
		return nil
	case "verify":
		index := f.String("index", "", "index.json to verify")
		public := f.String("public", "", "explicit public key (default: compiled-in keys)")
		if err := parse(f, args[1:]); err != nil {
			return err
		}
		if *index == "" {
			return fmt.Errorf("verify requires --index")
		}
		data, err := catalog.ReadFile(ctx, *index, catalog.MaxIndexSize)
		if err != nil {
			return err
		}
		sig, err := catalog.ReadFile(ctx, *index+".minisig", catalog.MaxSignatureSize)
		if err != nil {
			return err
		}
		var i *catalog.Index
		if *public != "" {
			b, err := catalog.ReadFile(ctx, *public, catalog.MaxSignatureSize)
			if err != nil {
				return err
			}
			key, err := catalog.ParsePublicKey(b)
			if err != nil {
				return err
			}
			i, err = catalog.VerifyIndexWithKeys(data, sig, []catalog.PublicKey{key})
			if err != nil {
				return err
			}
		} else {
			i, err = catalog.VerifyIndex(data, sig)
			if err != nil {
				return err
			}
		}
		fmt.Fprintf(out, "verified %s sequence %d\n", i.Channel, i.Sequence)
		return nil
	default:
		return fmt.Errorf("unknown command %q (expected keygen, build, sign or verify)", args[0])
	}
}

func parse(f *flag.FlagSet, args []string) error {
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return fmt.Errorf("unexpected argument: %s", f.Arg(0))
	}
	return nil
}

func createKey(ctx context.Context, path string, data []byte, mode os.FileMode) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	defer f.Close()
	ok := false
	defer func() {
		if !ok {
			os.Remove(path)
		}
	}()
	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	ok = true
	return nil
}
