// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-20

//go:build !nogui

package gui

import (
	"log"
	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/application"
	"golang.org/x/sys/windows"
)

const webView2Page = "https://developer.microsoft.com/microsoft-edge/webview2/"

const (
	mbYesNo         = 0x00000004
	mbIconError     = 0x00000010
	mbSetForeground = 0x00010000
	mbTopmost       = 0x00040000
	idYes           = 6
)

var procMessageBoxW = windows.NewLazySystemDLL("user32.dll").NewProc("MessageBoxW")

// webView2Check reports whether windows can be made. Without the WebView2 runtime Wails passes the
// failure to ErrorHandler and exits, so a double-click shows nothing; instead this asks Windows for
// a message box before any window exists, offering the download page.
func webView2Check(app *application.App, lang string) bool {
	if v, _ := app.Env.Info().PlatformInfo["WebView2"].(string); v != "" {
		return true
	}
	log.Printf("openxplay-flasher: the Microsoft Edge WebView2 Runtime is missing; install it from %s", webView2Page)
	title, body := webView2Notice(lang)
	r, _, _ := procMessageBoxW.Call(0,
		uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(body))),
		uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(title))),
		mbYesNo|mbIconError|mbSetForeground|mbTopmost)
	if int(r) == idYes {
		if err := windows.ShellExecute(0, windows.StringToUTF16Ptr("open"), windows.StringToUTF16Ptr(webView2Page), nil, nil, windows.SW_SHOWNORMAL); err != nil {
			log.Println("openxplay-flasher: opening the WebView2 page:", err)
		}
	}
	return false
}
