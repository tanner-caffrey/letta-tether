//go:build !windows

package main

import (
	"os"

	"github.com/tanner-caffrey/letta-tether/internal/update"
)

func execSelf(program string, args []string) error {
	return update.ReplaceSelf(program, args, os.Environ())
}
