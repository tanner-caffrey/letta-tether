//go:build !windows

package uninstall

// On Mac and Linux the app is a file the person put somewhere (Applications, Downloads).
// We leave it for them to drag to the Trash: deleting a running app bundle out from under
// the OS is worse than one leftover file. The page says so.
func removeSelf(exe string) error { return nil }
