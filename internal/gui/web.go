// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-20

package gui

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/adb"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/app"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/usb"
)

const webCookie = "oxpf_key"

// webNative backs the browser mode, where the page opens links itself.
type webNative struct{}

func (webNative) OpenURL(string) error { return errNoDialogs }

func (webNative) SaveFile(string, []byte) (string, error) { return "", errNoDialogs }

func (webNative) OpenFile() (string, []byte, error) { return "", nil, errNoDialogs }

func (webNative) OpenPackages() ([]string, error) { return nil, errNoDialogs }

// SetLang: the page titles the browser tab itself, and a browser has no app menu.
func (webNative) SetLang(string) {}

// ServeWeb serves the GUI to a browser on a loopback address until ctx ends, for UI work with the
// browser's developer tools. It prints the address to open, which carries a one-time key.
func ServeWeb(ctx context.Context, addr string, out io.Writer) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return fmt.Errorf("browser mode listens on loopback addresses only, not %q", host)
	}
	dir, err := ConfigDir()
	if err != nil {
		return err
	}
	bus := usb.NewHostBus()
	defer bus.Close()
	hub := NewHub()
	svc, err := app.NewForBus(bus, hub)
	if err != nil {
		return err
	}
	svc.SetADB(adb.NewConnector())
	svc.Start(ctx)
	defer svc.Stop()

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	key := make([]byte, 16)
	_, _ = rand.Read(key)
	keyHex := hex.EncodeToString(key)
	page := Handler(Deps{Service: svc, Hub: hub, Settings: NewSettingsStore(dir), Native: webNative{},
		SystemLang: systemLang(), Web: true})
	srv := &http.Server{
		Handler:           guardWeb(page, ln.Addr().String(), keyHex),
		ReadHeaderTimeout: 10 * time.Second,
	}
	fmt.Fprintf(out, "Open http://%s/?key=%s\n", ln.Addr(), keyHex)

	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// guardWeb admits a browser that opened the printed address: the key is traded for a cookie once,
// and every request must carry it. The Host check stops DNS rebinding; the strict cookie and the
// JSON-only API stop cross-site requests.
func guardWeb(next http.Handler, hostPort, key string) http.Handler {
	_, port, _ := net.SplitHostPort(hostPort)
	allowedHosts := map[string]bool{hostPort: true, "localhost:" + port: true}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !allowedHosts[r.Host] {
			http.Error(w, "unexpected host", http.StatusForbidden)
			return
		}
		if k := r.URL.Query().Get("key"); k != "" {
			if subtle.ConstantTimeCompare([]byte(k), []byte(key)) != 1 {
				http.Error(w, "wrong key", http.StatusForbidden)
				return
			}
			http.SetCookie(w, &http.Cookie{Name: webCookie, Value: key, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		c, err := r.Cookie(webCookie)
		if err != nil || subtle.ConstantTimeCompare([]byte(c.Value), []byte(key)) != 1 {
			http.Error(w, "open the address printed by openxplay-flasher web", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}
