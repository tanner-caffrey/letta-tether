// Package icons holds the tray icons: one per state (green connected, blue working,
// amber reconnecting, grey stopped, red error). Windows wants .ico, everyone else .png.
package icons

import (
	"embed"
	"runtime"
)

//go:embed *.ico *.png
var files embed.FS

// For returns the icon bytes for a state name.
func For(state string) []byte {
	ext := ".png"
	if runtime.GOOS == "windows" {
		ext = ".ico"
	}
	b, err := files.ReadFile(state + ext)
	if err != nil {
		b, _ = files.ReadFile("stopped" + ext)
	}
	return b
}
