package main

import (
	"fmt"
	"os"

	"github.com/AkkunYo/SyncHub/internal/config"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: validate-config <config.yaml>")
		os.Exit(2)
	}

	cfg, err := config.Load(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "config validation failed")
		os.Exit(1)
	}

	fmt.Printf("valid config: %d targets, %d upstreams\n", len(cfg.Targets), len(cfg.Upstreams))
}
