package main

import (
	"os"

	"github.com/stables/stables/internal/cli"
)

func main() { os.Exit(cli.Run(os.Args)) }
