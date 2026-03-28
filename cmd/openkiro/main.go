// Package main is the entry point for the openkiro CLI.
//
// Usage:
//
//	openkiro server [port]               - start the Anthropic API proxy (default port 1234)
//	openkiro sandbox create [flags]      - create an agent sandbox container
//	openkiro sandbox start   SESSION_ID  - start a created sandbox
//	openkiro sandbox exec    SESSION_ID  - exec a command in a running sandbox
//	openkiro sandbox stop    SESSION_ID  - stop a sandbox
//	openkiro sandbox destroy SESSION_ID  - destroy a sandbox container
//	openkiro sandbox list                - list all tracked sandboxes
//	openkiro version                     - print version info
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/ryolambert/openkiro/internal/proxy"
	"github.com/ryolambert/openkiro/internal/sandbox"
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

	case "sandbox":
		runSandbox(args[1:])

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

// runSandbox dispatches sandbox sub-commands.
func runSandbox(args []string) {
	if len(args) == 0 {
		printSandboxUsage()
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	mgr := sandbox.NewManager()

	switch args[0] {
	case "create":
		sandboxCreate(ctx, mgr, args[1:])
	case "start":
		sandboxStart(ctx, mgr, args[1:])
	case "stop":
		sandboxStop(ctx, mgr, args[1:])
	case "destroy":
		sandboxDestroy(ctx, mgr, args[1:])
	case "list":
		sandboxList(mgr)
	case "help", "--help", "-h":
		printSandboxUsage()
	default:
		fmt.Fprintf(os.Stderr, "sandbox: unknown sub-command: %s\n\n", args[0])
		printSandboxUsage()
		os.Exit(1)
	}
}

// sandboxCreate creates (and starts) a new agent sandbox container.
//
// Flags:
//
//	--id ID              Session identifier (required)
//	--image IMAGE        Docker image (default: openkiro-sandbox:latest)
//	--workspace DIR      Host path to bind-mount at /workspace
//	--network MODE       Docker network mode: none|bridge (default: bridge for agent)
//	--preset PRESET      Config preset: default|agent|claude|kiro (default: agent)
//	--env KEY=VALUE      Additional environment variable (repeatable)
func sandboxCreate(ctx context.Context, mgr *sandbox.Manager, args []string) {
	id := ""
	cfg := sandbox.AgentConfig()

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--id":
			i++
			if i < len(args) {
				id = args[i]
			}
		case "--image":
			i++
			if i < len(args) {
				cfg.Image = args[i]
			}
		case "--workspace":
			i++
			if i < len(args) {
				cfg.WorkspaceDir = args[i]
			}
		case "--network":
			i++
			if i < len(args) {
				cfg.NetworkMode = args[i]
			}
		case "--preset":
			i++
			if i < len(args) {
				switch args[i] {
				case "default":
					cfg = sandbox.DefaultConfig()
				case "agent":
					cfg = sandbox.AgentConfig()
				case "claude":
					cfg = sandbox.ClaudeCodeConfig()
				case "kiro":
					cfg = sandbox.KiroConfig()
				default:
					fmt.Fprintf(os.Stderr, "sandbox create: unknown preset %q\n", args[i])
					os.Exit(1)
				}
			}
		case "--env":
			i++
			if i < len(args) {
				cfg.Env = append(cfg.Env, args[i])
			}
		}
	}

	if id == "" {
		fmt.Fprintln(os.Stderr, "sandbox create: --id SESSION_ID is required")
		os.Exit(1)
	}

	sb, err := mgr.Create(ctx, id, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sandbox create: %v\n", err)
		os.Exit(1)
	}
	if err := mgr.Start(ctx, sb.ID); err != nil {
		fmt.Fprintf(os.Stderr, "sandbox start: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("sandbox %s created and started (container: %s, image: %s, network: %s)\n",
		sb.ID, sb.ContainerID, cfg.Image, cfg.NetworkMode)
}

func sandboxStart(ctx context.Context, mgr *sandbox.Manager, args []string) {
	id := requireID("start", args)
	if err := mgr.Start(ctx, id); err != nil {
		fmt.Fprintf(os.Stderr, "sandbox start: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("sandbox %s started\n", id)
}

func sandboxStop(ctx context.Context, mgr *sandbox.Manager, args []string) {
	id := requireID("stop", args)
	if err := mgr.Stop(ctx, id); err != nil {
		fmt.Fprintf(os.Stderr, "sandbox stop: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("sandbox %s stopped\n", id)
}

func sandboxDestroy(ctx context.Context, mgr *sandbox.Manager, args []string) {
	id := requireID("destroy", args)
	if err := mgr.Destroy(ctx, id); err != nil {
		fmt.Fprintf(os.Stderr, "sandbox destroy: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("sandbox %s destroyed\n", id)
}

func sandboxList(mgr *sandbox.Manager) {
	items := mgr.List()
	if len(items) == 0 {
		fmt.Println("(no sandboxes)")
		return
	}
	fmt.Printf("%-20s %-15s %-12s %s\n", "ID", "STATE", "CONTAINER", "IMAGE")
	fmt.Println(strings.Repeat("-", 70))
	for _, sb := range items {
		short := sb.ContainerID
		if len(short) > 12 {
			short = short[:12]
		}
		fmt.Printf("%-20s %-15s %-12s %s\n", sb.ID, sb.State, short, sb.Image)
	}
}

func requireID(subcmd string, args []string) string {
	if len(args) == 0 || args[0] == "" {
		fmt.Fprintf(os.Stderr, "sandbox %s: SESSION_ID is required\n", subcmd)
		os.Exit(1)
	}
	return args[0]
}

func printUsage() {
	fmt.Print(`openkiro - Anthropic API proxy for Kiro/AWS CodeWhisperer

Usage:
  openkiro server [port]       Start the proxy server (default port 1234).
                               Overridable via $OPENKIRO_PORT.
  openkiro sandbox <sub-cmd>   Manage ephemeral agent sandbox containers.
  openkiro version             Print version information.
  openkiro help                Show this help message.

Run 'openkiro sandbox help' for sandbox sub-commands.
`)
}

func printSandboxUsage() {
	fmt.Print(`openkiro sandbox — manage ephemeral agent sandbox containers

Sub-commands:
  create --id ID [flags]    Create and start a sandbox container.
  start  SESSION_ID         Start a stopped sandbox.
  stop   SESSION_ID         Stop a running sandbox (keep container).
  destroy SESSION_ID        Stop and remove a sandbox container.
  list                      List all tracked sandboxes.
  help                      Show this help.

create flags:
  --id ID              Session identifier (required).
  --image IMAGE        Docker image (default: openkiro-sandbox:latest).
  --workspace DIR      Host directory to bind-mount at /workspace.
  --network MODE       Network mode: none|bridge (default: bridge).
  --preset PRESET      Config preset: default|agent|claude|kiro.
  --env KEY=VALUE      Extra environment variable (repeatable).

Presets:
  default  Strict isolation: --network none, read-only root FS.
  agent    General agent workload: bridge networking, read-only root FS.
  claude   Claude Code: bridge + ANTHROPIC_BASE_URL/API_KEY env vars.
  kiro     Kiro agent: bridge + ANTHROPIC_BASE_URL/KIRO_PROXY env vars.

Examples:
  openkiro sandbox create --id dev-session --preset claude --workspace /my/project
  openkiro sandbox list
  openkiro sandbox destroy dev-session
`)
}

