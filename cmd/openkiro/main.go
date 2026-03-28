// Package main is the entry point for the openkiro CLI.
//
// Usage:
//
//	openkiro server [port]   - start the Anthropic API proxy (default port 1234)
//	openkiro version         - print version info
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/ryolambert/openkiro/internal/proxy"
)

// Injected at build time via -ldflags.
var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

func main() {
	args := os.Args[1:]

	if len(args) == 0 {
		printUsage()
		os.Exit(1)
	}

	switch args[0] {
	case "server":
		port := proxy.DefaultPort
		if len(args) > 1 {
			port = args[1]
		}
		// Allow $OPENKIRO_PORT to override the default.
		if p := os.Getenv("OPENKIRO_PORT"); p != "" {
			port = p
		}

		ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer cancel()
		proxy.StartServer(ctx, proxy.DefaultListenAddress, port)

	case "version", "--version", "-v":
		fmt.Printf("openkiro %s (commit: %s, built: %s)\n", version, commit, date)

	case "help", "--help", "-h":
		printUsage()

	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", args[0])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Print(`openkiro - Anthropic API proxy for Kiro/AWS CodeWhisperer

Usage:
  openkiro server [port]   Start the proxy server (default port 1234).
                           Overridable via $OPENKIRO_PORT.
  openkiro version         Print version information.
  openkiro help            Show this help message.
`)
}
