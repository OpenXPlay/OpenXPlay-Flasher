// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-20

package gui

// Strings shown by the native side: they appear before the page exists or outside it, so they live
// here rather than in the page's i18n.

// appTitle is the window title in the UI language.
func appTitle(lang string) string {
	if lang == LangChinese {
		return "OpenXPlay 刷机工具"
	}
	return "OpenXPlay Flasher"
}

// webView2Notice is the title and text of the box shown on Windows when WebView2 is missing. It
// comes before the page exists, so its strings live here rather than in the page's i18n.
func webView2Notice(lang string) (title, body string) {
	if lang == LangChinese {
		return "OpenXPlay 刷机工具无法启动",
			"本工具的窗口需要 Microsoft Edge WebView2 运行时，但这台电脑上没有安装。\n\n是否打开下载页面？安装完成后请重新打开本工具。"
	}
	return "OpenXPlay Flasher cannot start",
		"The flasher's window needs the Microsoft Edge WebView2 Runtime, which is not installed on this PC.\n\nOpen the download page? Start the flasher again after installing it."
}
