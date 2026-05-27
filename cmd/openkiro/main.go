// Package main is the entry point for the openkiro CLI.
//
// Usage:
//
//	openkiro server [port]               - start the Anthropic API proxy (default port 1234)
//	openkiro start [port]                - start the proxy as a background daemon
//	openkiro stop                        - stop the background daemon
//	openkiro status                      - show daemon status
//	openkiro read                        - print cached Kiro token data
//	openkiro refresh                     - refresh the Kiro token
//	openkiro export                      - print ANTHROPIC_* env var export lines
//	openkiro env                         - export env vars and write ~/.openkiro/credentials.json
//	openkiro token                       - print the current access token (for alias use)
//	openkiro alias [flags]               - generate/install shell aliases (okcc, oklaude)
//	openkiro claude                      - configure ~/.claude.json for openkiro
//	openkiro sandbox create [flags]      - create an agent sandbox container
//	openkiro sandbox start   SESSION_ID  - start a created sandbox
//	openkiro sandbox stop    SESSION_ID  - stop a sandbox
//	openkiro sandbox destroy SESSION_ID  - destroy a sandbox container
//	openkiro sandbox list                - list all tracked sandboxes
//	openkiro version                     - print version info
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"

	"github.com/ryolambert/openkiro/internal/daemon"
	"github.com/ryolambert/openkiro/internal/headroom"
	"github.com/ryolambert/openkiro/internal/middleware"
	"github.com/ryolambert/openkiro/internal/proxy"
	"github.com/ryolambert/openkiro/internal/sandbox"
	"github.com/ryolambert/openkiro/internal/token"
)

// Injected at build time via -ldflags.
var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

const openkiroBanner = ` ██████╗ ██████╗ ███████╗███╗   ██╗██╗  ██╗██╗██████╗  ██████╗
██╔═══██╗██╔══██╗██╔════╝████╗  ██║██║ ██╔╝██║██╔══██╗██╔═══██╗
██║   ██║██████╔╝█████╗  ██╔██╗ ██║█████╔╝ ██║██████╔╝██║   ██║
██║   ██║██╔═══╝ ██╔══╝  ██║╚██╗██║██╔═██╗ ██║██╔══██╗██║   ██║
╚██████╔╝██║     ███████╗██║ ╚████║██║  ██╗██║██║  ██║╚██████╔╝
 ╚═════╝ ╚═╝     ╚══════╝╚═╝  ╚═══╝╚═╝  ╚═╝╚═╝╚═╝  ╚═╝ ╚═════╝`

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
		if p := os.Getenv("OPENKIRO_PORT"); p != "" {
			port = p
		}
		ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer cancel()
		proxy.StartServer(ctx, proxy.DefaultListenAddress, port, buildMiddlewareChain(ctx))

	case "start":
		cmdStart(args[1:])

	case "stop":
		cmdStop()

	case "status":
		cmdStatus()

	case "read":
		token.ReadToken()

	case "refresh":
		token.RefreshToken()

	case "export":
		port := daemon.ParsePortFlag()
		token.ExportEnvVars(port)

	case "env":
		cmdEnv(args[1:])

	case "token":
		// Print just the access token (used by alias functions).
		t, err := token.GetToken()
		if err != nil {
			fmt.Fprintf(os.Stderr, "token: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(t.AccessToken)

	case "alias":
		cmdAlias(args[1:])

	case "claude":
		daemon.SetClaude()

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

// cmdStart starts the proxy as a background daemon process.
func cmdStart(args []string) {
	port := proxy.DefaultPort
	if len(args) > 0 {
		port = args[0]
	}
	if p := os.Getenv("OPENKIRO_PORT"); p != "" {
		port = p
	}

	if err := daemon.CleanStalePID(); err != nil {
		fmt.Fprintf(os.Stderr, "start: %v\n", err)
	}

	pid, err := daemon.ReadPID()
	if err == nil && daemon.IsRunning(pid) {
		fmt.Printf("openkiro already running (pid %d)\n", pid)
		return
	}

	self, err := daemon.SelfPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "start: %v\n", err)
		os.Exit(1)
	}

	logPath, err := daemon.LogFilePath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "start: %v\n", err)
		os.Exit(1)
	}

	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		fmt.Fprintf(os.Stderr, "start: open log: %v\n", err)
		os.Exit(1)
	}
	defer logFile.Close()

	//nolint:gosec // self is resolved from os.Executable, port is developer-controlled.
	cmd := exec.Command(self, "server", port)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = daemonSysProcAttr()
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "start: %v\n", err)
		os.Exit(1)
	}
	if err := daemon.WritePID(cmd.Process.Pid); err != nil {
		fmt.Fprintf(os.Stderr, "start: write pid: %v\n", err)
	}
	fmt.Printf("openkiro started (pid %d, port %s, log %s)\n", cmd.Process.Pid, port, logPath)
}

// cmdStop stops the background daemon.
func cmdStop() {
	pid, err := daemon.ReadPID()
	if err != nil {
		fmt.Fprintln(os.Stderr, "stop: no pid file found — is openkiro running?")
		os.Exit(1)
	}
	if !daemon.IsRunning(pid) {
		fmt.Println("openkiro is not running (stale pid file removed)")
		_ = daemon.RemovePID()
		return
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		fmt.Fprintf(os.Stderr, "stop: %v\n", err)
		os.Exit(1)
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		fmt.Fprintf(os.Stderr, "stop: %v\n", err)
		os.Exit(1)
	}
	_ = daemon.RemovePID()
	fmt.Printf("openkiro stopped (pid %d)\n", pid)
}

// cmdStatus prints whether the daemon is running.
func cmdStatus() {
	pid, err := daemon.ReadPID()
	if err != nil {
		fmt.Println("openkiro: not running")
		return
	}
	if daemon.IsRunning(pid) {
		port, _ := daemon.ResolvePort("")
		fmt.Printf("openkiro: running (pid %d, port %s)\n", pid, port)
	} else {
		fmt.Println("openkiro: not running (stale pid file)")
		_ = daemon.CleanStalePID()
	}
}

// cmdEnv writes credentials to ~/.openkiro/credentials.json and prints export lines.
func cmdEnv(args []string) {
	port := daemon.ParsePortFlag()
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "--port" || args[i] == "-port" {
			port = args[i+1]
		}
	}
	resolvedPort, err := daemon.ResolvePort(port)
	if err != nil {
		fmt.Fprintf(os.Stderr, "env: %v\n", err)
		os.Exit(1)
	}
	t, err := token.GetToken()
	if err != nil {
		fmt.Fprintf(os.Stderr, "env: %v\n", err)
		os.Exit(1)
	}
	baseURL := "http://localhost:" + resolvedPort
	if err := token.WriteCredentials(baseURL, t.AccessToken); err != nil {
		fmt.Fprintf(os.Stderr, "env: write credentials: %v\n", err)
	}
	token.ExportEnvVars(resolvedPort)
}

// cmdAlias generates or installs shell alias functions.
func cmdAlias(args []string) {
	shell := ""
	port := proxy.DefaultPort
	names := daemon.DefaultAliasNames()
	install := false

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--shell":
			i++
			if i < len(args) {
				shell = args[i]
			}
		case "--port":
			i++
			if i < len(args) {
				port = args[i]
			}
		case "--name":
			i++
			if i < len(args) {
				names = strings.Split(args[i], ",")
			}
		case "--install":
			install = true
		}
	}

	if shell == "" {
		shell = daemon.DetectShell()
	}

	self, err := daemon.SelfPath()
	if err != nil {
		self = "openkiro"
	}

	snippet := daemon.GenerateAliases(shell, self, port, names)

	if !install {
		fmt.Println(snippet)
		return
	}

	path, err := daemon.InstallAlias(shell, snippet)
	if err != nil {
		fmt.Fprintf(os.Stderr, "alias --install: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("alias installed to %s\nReload with: source %s\n", path, path)
}

func printUsage() {
	fmt.Printf(`%s

openkiro - Anthropic API proxy for Kiro/AWS CodeWhisperer

Usage:
  openkiro server [port]       Start the proxy server (foreground, default port 1234).
                               Overridable via $OPENKIRO_PORT.
  openkiro start [port]        Start the proxy as a background daemon.
  openkiro stop                Stop the background daemon.
  openkiro status              Show daemon status.
  openkiro read                Print cached Kiro token data.
  openkiro refresh             Refresh the Kiro token.
  openkiro export              Print ANTHROPIC_* env var export lines.
  openkiro env [--port PORT]   Export env vars and write ~/.openkiro/credentials.json.
  openkiro token               Print the current access token.
  openkiro alias [flags]       Generate/install shell aliases (okcc, oklaude).
    --name NAME                Alias name (default: okcc,oklaude).
    --shell SHELL              Shell type: bash|zsh|powershell|cmd (auto-detected).
    --port PORT                Port for generated alias (default: 1234).
    --install                  Write alias to shell config file.
  openkiro claude              Configure ~/.claude.json for openkiro.
  openkiro sandbox <sub-cmd>   Manage ephemeral agent sandbox containers.
  openkiro version             Print version information.
  openkiro help                Show this help message.

Run 'openkiro sandbox help' for sandbox sub-commands.
`, openkiroBanner)
}

func printSandboxUsage() {
	fmt.Printf(`%s

openkiro sandbox — manage ephemeral agent sandbox containers

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
`, openkiroBanner)
}

// buildMiddlewareChain constructs the proxy middleware chain.
// Headroom compression is enabled when OPENKIRO_HEADROOM=1.
// Returns nil if no middleware is active (proxy runs passthrough).
func buildMiddlewareChain(ctx context.Context) proxy.RequestProcessor {
	if os.Getenv("OPENKIRO_HEADROOM") != "1" {
		return nil
	}
	cfg := headroom.DefaultConfig()
	mgr := headroom.NewManager(cfg)
	if !mgr.Installed() {
		log.Printf("headroom: binary not found — skipping (install with: pip install %q)", cfg.PipPackage)
		return nil
	}
	if err := mgr.Start(ctx); err != nil {
		log.Printf("headroom: failed to start proxy: %v — continuing without compression", err)
		return nil
	}
	go func() {
		<-ctx.Done()
		if err := mgr.Stop(); err != nil {
			log.Printf("headroom: stop error: %v", err)
		}
	}()
	chain := &middleware.Chain{}
	chain.Add(middleware.NewHeadroomMiddleware(headroom.NewClient(cfg), true))
	log.Printf("headroom: compression middleware active on /v1/messages")
	return chain
}
