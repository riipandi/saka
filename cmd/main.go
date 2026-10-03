package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/riipandi/saka/pkg/printext"
)

func main() {
	// The context every command runs under is the one the operating system
	// cancels: SIGINT and SIGTERM trigger the graceful drain instead of
	// killing the process, so a run releases what it holds — the listener,
	// the queue workers, the pool — in the order its shutdown expects. The
	// drain itself is bounded by its timeout.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := rootCmd.Run(ctx, os.Args); err != nil {
		p := printext.NewPalette(os.Stderr)
		_ = p.Printf("%s\n", p.Red(err.Error()))
		os.Exit(1)
	}
}
