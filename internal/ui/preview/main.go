//go:build ignore

// go run internal/ui/preview/main.go : serves the page with the real handlers for a screenshot.
package main

import (
	"fmt"
	"os"
	"time"

	"github.com/tanner-caffrey/letta-tether/internal/config"
	"github.com/tanner-caffrey/letta-tether/internal/runner"
	"github.com/tanner-caffrey/letta-tether/internal/ui"
)

func main() {
	a := &ui.App{Runner: &runner.Runner{}, Apply: func(config.Config) error { return nil }}
	u, _ := a.Start(config.Load())
	fmt.Println(u)
	os.Stdout.Sync()
	time.Sleep(60 * time.Second)
}
