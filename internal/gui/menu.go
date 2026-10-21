// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-20

//go:build !nogui

package gui

import (
	"github.com/wailsapp/wails/v3/pkg/application"
)

// macMenu is the macOS menu bar in the UI language. It replaces Wails' default menu, whose Help
// item loads wails.io into the app's own window; Help here opens pages in the browser.
func macMenu(lang string, openURL func(string), showSettings func()) *application.Menu {
	s := menuStrings(lang)
	label := func(m *application.Menu, role application.Role, text string) {
		m.AddRole(role)
		if item := m.FindByRole(role); item != nil {
			item.SetLabel(text)
		}
	}
	bar := application.NewMenu()

	app := bar.AddSubmenu(appTitle(lang))
	label(app, application.About, s.About)
	app.AddSeparator()
	app.Add(s.Settings).SetAccelerator("CmdOrCtrl+,").OnClick(func(*application.Context) { showSettings() })
	app.AddSeparator()
	label(app, application.Hide, s.Hide)
	label(app, application.HideOthers, s.HideOthers)
	label(app, application.UnHide, s.ShowAll)
	app.AddSeparator()
	label(app, application.Quit, s.Quit)

	edit := bar.AddSubmenu(s.Edit)
	label(edit, application.Undo, s.Undo)
	label(edit, application.Redo, s.Redo)
	edit.AddSeparator()
	label(edit, application.Cut, s.Cut)
	label(edit, application.Copy, s.Copy)
	label(edit, application.Paste, s.Paste)
	label(edit, application.SelectAll, s.SelectAll)

	window := bar.AddSubmenu(s.Window)
	label(window, application.Minimise, s.Minimise)
	label(window, application.Zoom, s.Zoom)

	help := bar.AddSubmenu(s.Help)
	links := linksFor(lang)
	help.Add(s.Tutorial).OnClick(func(*application.Context) { openURL(links.Tutorial) })
	help.Add(s.Download).OnClick(func(*application.Context) { openURL(links.Download) })
	help.Add(s.Website).OnClick(func(*application.Context) { openURL(links.Website) })
	help.Add(s.Source).OnClick(func(*application.Context) { openURL(links.Source) })
	return bar
}
