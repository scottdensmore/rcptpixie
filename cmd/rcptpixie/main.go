package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/scottdensmore/rcptpixie/internal/cli"
)

func main() {
	// The signal context threads into every ollama request, so Ctrl-C cancels an
	// in-flight generation instead of waiting out the request timeout.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	os.Exit(int(cli.Run(ctx, cli.Env{
		Args:   os.Args[1:],
		Stdout: os.Stdout,
		Stderr: os.Stderr,
		Stdin:  os.Stdin,
		Getenv: os.Getenv,
		IsTTY:  cli.IsTerminal,
	})))
}
