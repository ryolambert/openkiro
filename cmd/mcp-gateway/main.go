// mcp-gateway — Docker MCP Gateway HTTP server for openkiro agent sandboxes.
//
// Discovers MCP (Model Context Protocol) tool servers running as Docker
// containers (via the mcp.enable=true label) and exposes them through a
// unified HTTP API. Agent workloads query this gateway to discover available
// tools without needing to know individual container addresses.
//
// Usage:
//
//	mcp-gateway serve [--port PORT]   Start the gateway server (default: 8081).
//	mcp-gateway list                  List currently discovered servers (one-shot).
//	mcp-gateway version               Print version.
//	mcp-gateway help                  Show this help.
//
// HTTP API (mcp-gateway serve):
//
//	GET  /health           Liveness probe; checks Docker daemon connectivity.
//	GET  /servers          List all discovered MCP servers as JSON.
//	POST /discover         Trigger an immediate re-discovery scan.
//	GET  /tools?server=X   Get the HTTP endpoint URL for a named server.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ryolambert/openkiro/internal/gateway"
)

const defaultPort = "8081"

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		printUsage()
		os.Exit(1)
	}

	switch args[0] {
	case "serve":
		port := defaultPort
		for i := 0; i < len(args)-1; i++ {
			if args[i] == "--port" || args[i] == "-p" {
				port = args[i+1]
			}
		}
		runServer(port)

	case "list":
		runList()

	case "version", "--version", "-v":
		fmt.Println("mcp-gateway v0.1.0 (openkiro Docker MCP Gateway)")

	case "help", "--help", "-h":
		printUsage()

	default:
		fmt.Fprintf(os.Stderr, "mcp-gateway: unknown command %q\n\n", args[0])
		printUsage()
		os.Exit(1)
	}
}

// runServer starts the gateway HTTP server and background discovery loop.
func runServer(port string) {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	gw := gateway.NewGateway()

	// Perform an initial discovery before accepting requests.
	if _, err := gw.Discover(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "mcp-gateway: initial discover: %v\n", err)
	}
	go gw.StartDiscovery(ctx)

	mux := http.NewServeMux()

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		if err := gw.Health(r.Context()); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			writeJSON(w, map[string]string{"status": "error", "detail": err.Error()})
			return
		}
		writeJSON(w, map[string]string{"status": "ok", "tool": "mcp-gateway"})
	})

	mux.HandleFunc("/servers", func(w http.ResponseWriter, _ *http.Request) {
		servers := gw.Servers()
		writeJSON(w, map[string]any{"servers": servers, "count": len(servers)})
	})

	mux.HandleFunc("/discover", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST required", http.StatusMethodNotAllowed)
			return
		}
		servers, err := gw.Discover(r.Context())
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			writeJSON(w, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, map[string]any{"servers": servers, "count": len(servers)})
	})

	mux.HandleFunc("/tools", func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Query().Get("server")
		if name == "" {
			http.Error(w, `{"error":"server query param required"}`, http.StatusBadRequest)
			return
		}
		endpoint, err := gw.ToolEndpoint(name)
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			writeJSON(w, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, map[string]string{"server": name, "endpoint": endpoint})
	})

	addr := "127.0.0.1:" + port
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
	}

	fmt.Fprintf(os.Stderr, "mcp-gateway: listening on %s\n", addr)
	go func() {
		<-ctx.Done()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fmt.Fprintf(os.Stderr, "mcp-gateway: server error: %v\n", err)
		os.Exit(1)
	}
}

// runList performs a one-shot discovery and prints the found servers.
func runList() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	gw := gateway.NewGateway()
	servers, err := gw.Discover(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mcp-gateway: discover: %v\n", err)
		os.Exit(1)
	}

	if len(servers) == 0 {
		fmt.Println("(no MCP servers found — are any containers running with mcp.enable=true?)")
		return
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(map[string]any{"servers": servers, "count": len(servers)})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	_ = enc.Encode(v)
}

func printUsage() {
	fmt.Print(`mcp-gateway — Docker MCP Gateway server

Usage:
  mcp-gateway serve [--port PORT]  Start the gateway server (default port 8081).
  mcp-gateway list                 List discovered MCP servers (one-shot).
  mcp-gateway version              Print version.
  mcp-gateway help                 Show this help.

HTTP API (mcp-gateway serve):
  GET  /health              Docker daemon liveness probe.
  GET  /servers             List all discovered MCP servers.
  POST /discover            Trigger immediate re-discovery.
  GET  /tools?server=NAME   Get tool endpoint URL for a named server.

Docker label format (on MCP server containers):
  mcp.enable=true
  mcp.name=my-tools
  mcp.transport=http     (or stdio)
  mcp.port=9090          (default: 8080)
  mcp.path=/mcp          (default: /mcp)

Examples:
  mcp-gateway serve --port 8081
  mcp-gateway list
  curl http://127.0.0.1:8081/servers
  curl http://127.0.0.1:8081/tools?server=my-tools
`)
}
