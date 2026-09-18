package main

import (
	"fmt"
	"os"

	"playplace/internal/cli"
)

func main() {
	if err := cli.New().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
