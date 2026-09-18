// Command stab is the stab client and local controller.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/stables/stab/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	app, err := cli.New("", os.Stdin, os.Stdout, os.Stderr)
	if err != nil {
		os.Stderr.WriteString("stab: " + err.Error() + "\n")
		os.Exit(1)
	}
	defer app.Close()
	os.Exit(app.Run(ctx, os.Args[1:]))
}
