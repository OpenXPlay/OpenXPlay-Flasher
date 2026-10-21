// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

//go:build !nogui

package gui

import (
	"context"
	_ "embed"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/adb"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/app"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/config"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/power"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/stage"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/usb"
	"github.com/OpenXPlay/OpenXPlay-Flasher/internal/version"
)

//go:embed icon.png
var appIcon []byte

// runtimeIcon is the icon handed to Wails: none on macOS, where the bundle's .icns (drawn on
// Apple's icon grid) is the Dock icon and a runtime icon would replace it at full bleed.
func runtimeIcon() []byte {
	if runtime.GOOS == "darwin" {
		return nil
	}
	return appIcon
}

// host is the Wails application: it owns the window and implements Native.
type host struct {
	app      *application.App
	guard    *closeGuard
	critical func() bool
	mu       sync.Mutex
	lang     string
}

func (h *host) OpenURL(url string) error {
	return h.app.Browser.OpenURL(url)
}

// SetLang applies the UI language to the native parts: the window title (Wails does not follow
// the page's document.title) and, on macOS, the menu bar.
func (h *host) SetLang(lang string) {
	h.mu.Lock()
	h.lang = lang
	h.mu.Unlock()
	application.InvokeAsync(func() {
		if w, ok := h.app.Window.GetByName("main"); ok {
			w.SetTitle(appTitle(lang))
		}
		if runtime.GOOS == "darwin" {
			h.app.Menu.Set(macMenu(lang, h.openInBrowser, h.showSettings))
		}
	})
}

func (h *host) confirmQuit(answer func(bool)) {
	h.mu.Lock()
	lang := h.lang
	h.mu.Unlock()
	s := quitStrings(lang)
	if h.critical != nil && h.critical() {
		s.Warning = s.Critical
	}
	dialog := h.app.Dialog.Warning().SetTitle(appTitle(lang)).SetMessage(s.Warning)
	dialog.AddButton(s.Keep).SetAsDefault().SetAsCancel().OnClick(func() { answer(false) })
	dialog.AddButton(s.Quit).OnClick(func() { answer(true) })
	dialog.Show()
}

func (h *host) SaveFile(name string, data []byte) (string, error) {
	dest, err := h.app.Dialog.SaveFile().SetFilename(name).CanCreateDirectories(true).PromptForSingleSelection()
	if err != nil || dest == "" {
		return "", err
	}
	return dest, os.WriteFile(dest, data, 0o600)
}

func (h *host) OpenFile() (string, []byte, error) {
	src, err := h.app.Dialog.OpenFile().CanChooseFiles(true).PromptForSingleSelection()
	if err != nil || src == "" {
		return "", nil, err
	}
	st, err := os.Stat(src)
	if err != nil {
		return "", nil, err
	}
	if st.Size() > config.MaxSize {
		return "", nil, config.ErrTooLarge
	}
	data, err := os.ReadFile(src)
	return filepath.Base(src), data, err
}

func (h *host) OpenPackages(layer string) ([]string, error) {
	patterns := stage.PackageFileTypes(layer)
	for i := range patterns {
		patterns[i] = "*" + patterns[i]
	}
	return h.app.Dialog.OpenFile().CanChooseFiles(true).
		AddFilter(strings.Join(patterns, ", "), strings.Join(patterns, ";")).PromptForMultipleSelection()
}

func (h *host) OpenIdentity() ([]string, error) {
	return h.app.Dialog.OpenFile().CanChooseFiles(true).PromptForMultipleSelection()
}
func (h *host) OpenSSHKey() (string, error) {
	return h.app.Dialog.OpenFile().SetTitle("SSH private key / SSH 私钥").PromptForSingleSelection()
}

func (h *host) OpenArtifact() (string, error) {
	return h.app.Dialog.OpenFile().CanChooseFiles(true).PromptForSingleSelection()
}
func (h *host) OpenArtifacts() ([]string, error) {
	return h.app.Dialog.OpenFile().CanChooseFiles(true).PromptForMultipleSelection()
}
func (h *host) OpenBackupFolder() (string, error) {
	return h.app.Dialog.OpenFile().CanChooseFiles(false).CanChooseDirectories(true).PromptForSingleSelection()
}
func (h *host) ShowFolder(path string) error { return h.app.Browser.OpenFile(path) }

func (h *host) openInBrowser(url string) {
	if err := h.OpenURL(url); err != nil {
		log.Println("openxplay-flasher: opening", url+":", err)
	}
}

func (h *host) showSettings() {
	application.InvokeAsync(func() {
		if w, ok := h.app.Window.GetByName("main"); ok {
			w.Show().Focus()
			w.ExecJS("window.location.hash = 'settings'")
		}
	})
}

// Run starts the desktop app and returns when its window is closed.
func Run() error {
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
	if err := svc.ConfigureDefaultStorage(); err != nil {
		return err
	}
	svc.SetADB(adb.NewConnector())
	svc.SetSleepInhibitor(power.NewSystem())
	settings := NewSettingsStore(dir)
	sysLang := systemLang()
	lang := UILang(settings.Load().Lang, sysLang)
	h := &host{lang: lang, critical: svc.InstallCritical}
	h.guard = &closeGuard{running: svc.InstallRunning, prompt: h.confirmQuit, quit: func() { h.app.Quit() }}

	h.app = application.New(application.Options{
		Name:        version.Name,
		Description: "Installs and updates the OpenXPlay relay on a USB dongle",
		Icon:        runtimeIcon(),
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
		ShouldQuit:     h.guard.allow,
		// Wails exits on some webview errors unless they are handled here.
		ErrorHandler: func(err error) { log.Println("openxplay-flasher:", err) },
	})

	if !webView2Check(h.app, lang) {
		return nil
	}

	if runtime.GOOS == "darwin" {
		h.app.Menu.Set(macMenu(lang, h.openInBrowser, h.showSettings))
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
	win.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) {
		if !h.guard.allow() {
			event.Cancel()
		}
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
