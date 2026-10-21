// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-20

//go:build !nogui

package gui

import (
	"context"
	_ "embed"
	"log"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/app"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/usb"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/version"
)

//go:embed icon.png
var appIcon []byte

// host is the Wails application: it owns the window and implements Native.
type host struct {
	app *application.App
}

func (h *host) OpenURL(url string) error {
	return h.app.Browser.OpenURL(url)
}

// SetTitle renames the main window; Wails does not follow the page's document.title.
func (h *host) SetTitle(title string) {
	application.InvokeAsync(func() {
		if w, ok := h.app.Window.GetByName("main"); ok {
			w.SetTitle(title)
		}
	})
}

// Run starts the desktop app and returns when its window is closed.
func Run() error {
	dir, err := ConfigDir()
	if err != nil {
		return err
	}
	bus := usb.NewLibusbBus()
	defer bus.Close()
	hub := NewHub()
	svc, err := app.NewForBus(bus, hub)
	if err != nil {
		return err
	}
	settings := NewSettingsStore(dir)
	sysLang := systemLang()
	lang := UILang(settings.Load().Lang, sysLang)
	h := &host{}

	h.app = application.New(application.Options{
		Name:        version.Name,
		Description: "Installs and updates the OpenXPlay relay on a USB dongle",
		Icon:        appIcon,
		Assets: application.AssetOptions{Handler: Handler(Deps{
			Service:    svc,
			Hub:        hub,
			Settings:   settings,
			Native:     h,
			SystemLang: sysLang,
		})},
		Mac: application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: true},
		// The portable exe would otherwise keep its WebView2 profile in a folder named after the
		// exe file, which changes with every version.
		Windows:        application.WindowsOptions{WebviewUserDataPath: filepath.Join(dir, "WebView2")},
		SingleInstance: singleInstance(h),
		OnShutdown:     svc.Stop,
		// Wails exits on some webview errors unless they are handled here.
		ErrorHandler: func(err error) { log.Println("openxplay-flasher:", err) },
	})

	if !webView2Check(h.app, lang) {
		return nil
	}

	win := h.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             "main",
		Title:            appTitle(lang),
		Width:            960,
		Height:           680,
		MinWidth:         720,
		MinHeight:        520,
		URL:              "/",
		Hidden:           true,
		BackgroundColour: windowBackground(h.app.Env.IsDarkMode()),
	})
	showWhenReady(win)

	svc.Start(context.Background())
	return h.app.Run()
}

// windowBackground matches the page's --bg, so the window does not flash before the page draws.
func windowBackground(dark bool) application.RGBA {
	if dark {
		return application.NewRGB(0x14, 0x16, 0x18)
	}
	return application.NewRGB(0xf5, 0xf6, 0xf7)
}

// showWhenReady shows the window once its page can draw. A WebView2 window shown before its first
// navigation completes is drawn black, and Wails shows it after a fixed delay whether or not
// WebView2 has made its controller yet; other platforms show at once.
func showWhenReady(w *application.WebviewWindow) {
	if runtime.GOOS != "windows" {
		w.Show()
		return
	}
	var once sync.Once
	w.OnWindowEvent(events.Windows.WebViewNavigationCompleted, func(*application.WindowEvent) {
		once.Do(func() { application.InvokeAsync(func() { w.Show().Focus() }) })
	})
}

// singleInstance keeps one flasher per user on Windows; a second launch brings the first window
// forward. macOS already reuses a running app bundle.
func singleInstance(h *host) *application.SingleInstanceOptions {
	if runtime.GOOS != "windows" {
		return nil
	}
	return &application.SingleInstanceOptions{
		UniqueID: "org.openxplay.flasher",
		OnSecondInstanceLaunch: func(application.SecondInstanceData) {
			application.InvokeAsync(func() {
				if w, ok := h.app.Window.GetByName("main"); ok {
					w.Show()
					w.Focus()
				}
			})
		},
	}
}
