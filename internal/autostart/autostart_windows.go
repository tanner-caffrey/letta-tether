// Package autostart turns "start when I log in" on and off, the simplest undoable way per OS.
package autostart

import (
	"os"

	"golang.org/x/sys/windows/registry"
)

// Windows: one value under HKCU\...\Run. No admin rights, and it shows up in
// Task Manager's Startup tab where people can turn it off themselves.
const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`
const valueName = "Letta Tether"

func Set(on bool) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE|registry.QUERY_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if !on {
		err := k.DeleteValue(valueName)
		if err == registry.ErrNotExist {
			return nil
		}
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return k.SetStringValue(valueName, `"`+exe+`" --background`)
}

func IsSet() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	_, _, err = k.GetStringValue(valueName)
	return err == nil
}
