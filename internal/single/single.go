// Package single makes sure only one Letta Tether runs, and lets a second launch
// ("I double-clicked it again") ask the first one to open its page.
package single

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var ErrRunning = errors.New("already running")

type Lock struct {
	f    *os.File
	path string
}

func (l *Lock) Release() {
	if l == nil {
		return
	}
	unlock(l.f)
	l.f.Close()
	os.Remove(l.path)
}

// Acquire takes an exclusive lock on path, or returns ErrRunning.
func Acquire(path string) (*Lock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := lock(f); err != nil {
		f.Close()
		return nil, ErrRunning
	}
	return &Lock{f: f, path: path}, nil
}

func pokeFile(dir string) string { return filepath.Join(dir, "poke.addr") }

// Listen accepts pokes on a local port and calls f for each one.
func Listen(dir string, f func()) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return
	}
	_ = os.WriteFile(pokeFile(dir), []byte(ln.Addr().String()), 0o644)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			buf := make([]byte, 16)
			c.SetReadDeadline(time.Now().Add(2 * time.Second))
			n, _ := c.Read(buf)
			c.Close()
			if string(buf[:n]) == "open" {
				f()
			}
		}
	}()
}

// Poke asks the running copy to open its page.
func Poke(dir string) {
	b, err := os.ReadFile(pokeFile(dir))
	if err != nil {
		return
	}
	c, err := net.DialTimeout("tcp", strings.TrimSpace(string(b)), 2*time.Second)
	if err != nil {
		return
	}
	c.Write([]byte("open"))
	c.Close()
}
