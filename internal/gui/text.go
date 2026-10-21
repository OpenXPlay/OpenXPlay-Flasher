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

// siteURL is the project website in the UI language.
func siteURL(lang string) string {
	if lang == LangChinese {
		return "https://openxplay.org/"
	}
	return "https://openxplay.org/en/"
}

// menuText holds the macOS menu labels in one language.
type menuText struct {
	About, Hide, HideOthers, ShowAll, Quit        string
	Edit, Undo, Redo, Cut, Copy, Paste, SelectAll string
	Window, Minimise, Zoom, Help, Website, Source string
}

func menuStrings(lang string) menuText {
	if lang == LangChinese {
		return menuText{
			About: "关于 OpenXPlay 刷机工具", Hide: "隐藏 OpenXPlay 刷机工具", HideOthers: "隐藏其他", ShowAll: "全部显示",
			Quit: "退出 OpenXPlay 刷机工具", Edit: "编辑", Undo: "撤销", Redo: "重做", Cut: "剪切", Copy: "拷贝",
			Paste: "粘贴", SelectAll: "全选", Window: "窗口", Minimise: "最小化", Zoom: "缩放", Help: "帮助",
			Website: "OpenXPlay 网站", Source: "源代码",
		}
	}
	return menuText{
		About: "About OpenXPlay Flasher", Hide: "Hide OpenXPlay Flasher", HideOthers: "Hide Others", ShowAll: "Show All",
		Quit: "Quit OpenXPlay Flasher", Edit: "Edit", Undo: "Undo", Redo: "Redo", Cut: "Cut", Copy: "Copy",
		Paste: "Paste", SelectAll: "Select All", Window: "Window", Minimise: "Minimize", Zoom: "Zoom", Help: "Help",
		Website: "OpenXPlay Website", Source: "Source Code",
	}
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
