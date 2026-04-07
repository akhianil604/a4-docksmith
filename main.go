package main

import (
	"fmt"
	"os"

	"docksmith/internal/cli"
	"docksmith/isolation"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == isolation.InternalChildArg {
		os.Exit(isolation.ChildMain())
	}

	if err := cli.RootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
