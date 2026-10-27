package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/signal"

	"github.com/koltyakov/decide/internal/cli"
	"github.com/koltyakov/decide/internal/fault"
)

var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	err := cli.New(version, os.Stdin, os.Stdout, os.Stderr).ExecuteContext(ctx)
	if err == nil {
		return
	}
	code := fault.Code(err)
	if errors.Is(ctx.Err(), context.Canceled) {
		code = 130
	}
	if code != fault.Rejected {
		_ = json.NewEncoder(os.Stderr).Encode(map[string]any{"ok": false, "error": err.Error(), "code": code})
	}
	os.Exit(code)
}
