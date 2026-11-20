// Copyright (C) 2023-2026 Òscar Casajuana Alonso

package main

import "github.com/wailsapp/wails/v2/pkg/options"

// The window's background shows until the page paints its own, so a dark
// system would flash the light colour first. These are the page's --bg in
// styles.css for each scheme; theme_test.go keeps them in step with that file.
var (
	lightBackground = options.RGBA{R: 0xf4, G: 0xf3, B: 0xf0, A: 0xff}
	darkBackground  = options.RGBA{R: 0x12, G: 0x12, B: 0x14, A: 0xff}
)

func validTheme(theme string) bool {
	return theme == "" || theme == "light" || theme == "dark"
}

// startupBackground matches the saved appearance before the webview paints.
func startupBackground(theme string) *options.RGBA {
	bg := lightBackground
	if theme == "dark" || (theme == "" && systemPrefersDark()) {
		bg = darkBackground
	}

	return &bg
}
