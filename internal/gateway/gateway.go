// Package gateway implements the Docker MCP Gateway, which discovers MCP
// (Model Context Protocol) server endpoints running as Docker containers and
// routes tool-listing and tool-call requests to them.
//
// Containers advertise themselves by setting specific Docker labels:
//
//	mcp.enable=true          – opt the container in to tool discovery
//	mcp.name=<name>          – human-readable tool server name
//	mcp.transport=<http|stdio> – transport protocol (default: http)
//	mcp.port=<port>          – TCP port for the HTTP transport (default: 8080)
//	mcp.path=<path>          – HTTP path prefix (default: /mcp)
//
// The Gateway periodically re-discovers available containers so that
// tool servers that come and go are reflected without a restart.
package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Transport identifies how the MCP server communicates.
type Transport string

const (
	TransportHTTP  Transport = "http"
	TransportStdio Transport = "stdio"

	defaultPort      = "8080"
	defaultPath      = "/mcp"
	defaultTransport = TransportHTTP

	discoveryInterval = 30 * time.Second
)

// Tool describes a single tool exposed by an MCP server.
type Tool struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	ServerName  string `json:"server_name"`
}

// Server represents a discovered MCP tool server running in a container.
type Server struct {
	// ContainerID is the short Docker container ID.
	ContainerID string
	// Name is the value of the mcp.name label.
	Name string
	// Transport is how to communicate with the server.
	Transport Transport
	// Address is the host:port for HTTP transport.
	Address string
	// Path is the HTTP path prefix for HTTP transport.
	Path string
}

// DockerRunner abstracts Docker CLI execution for testability.
type DockerRunner interface {
	Run(ctx context.Context, args ...string) ([]byte, error)
}

// cliRunner executes Docker commands via the host docker binary.
type cliRunner struct{}

func (r *cliRunner) Run(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "docker", args...)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("docker %s: %w: %s", args[0], err, strings.TrimSpace(errBuf.String()))
	}
	return out.Bytes(), nil
}

// Gateway discovers and routes requests to MCP servers in Docker containers.
type Gateway struct {
	mu      sync.RWMutex
	servers map[string]*Server // keyed by container ID
	docker  DockerRunner
}

// NewGateway creates a Gateway backed by the host Docker CLI.
func NewGateway() *Gateway {
	return newGatewayWithRunner(&cliRunner{})
}

// newGatewayWithRunner creates a Gateway with a custom DockerRunner (for testing).
func newGatewayWithRunner(runner DockerRunner) *Gateway {
	return &Gateway{
		servers: make(map[string]*Server),
		docker:  runner,
	}
}

// containerInspect is the subset of docker inspect output we care about.
type containerInspect struct {
	ID     string `json:"Id"`
	Config struct {
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
	NetworkSettings struct {
		Ports map[string][]struct {
			HostIP   string `json:"HostIp"`
			HostPort string `json:"HostPort"`
		} `json:"Ports"`
	} `json:"NetworkSettings"`
	State struct {
		Running bool `json:"Running"`
	} `json:"State"`
}

// Discover finds all running Docker containers that have mcp.enable=true and
// updates the gateway's server registry. Returns the discovered servers.
func (g *Gateway) Discover(ctx context.Context) ([]*Server, error) {
	// List containers with the mcp.enable=true label.
	out, err := g.docker.Run(ctx,
		"ps", "--filter", "label=mcp.enable=true", "--format", "{{.ID}}")
	if err != nil {
		return nil, fmt.Errorf("gateway discover: list containers: %w", err)
	}

	ids := parseLines(out)
	if len(ids) == 0 {
		g.mu.Lock()
		g.servers = make(map[string]*Server)
		g.mu.Unlock()
		return nil, nil
	}

	// Inspect each container to extract labels and network info.
	inspectArgs := append([]string{"inspect"}, ids...)
	inspectOut, err := g.docker.Run(ctx, inspectArgs...)
	if err != nil {
		return nil, fmt.Errorf("gateway discover: inspect: %w", err)
	}

	var containers []containerInspect
	if err := json.Unmarshal(inspectOut, &containers); err != nil {
		return nil, fmt.Errorf("gateway discover: parse inspect: %w", err)
	}

	servers := make(map[string]*Server, len(containers))
	for i := range containers {
		c := &containers[i]
		if !c.State.Running {
			continue
		}
		srv := serverFromInspect(c)
		if srv == nil {
			continue
		}
		servers[c.ID] = srv
	}

	g.mu.Lock()
	g.servers = servers
	g.mu.Unlock()

	result := make([]*Server, 0, len(servers))
	for _, s := range servers {
		result = append(result, s)
	}
	return result, nil
}

// serverFromInspect builds a Server from container inspect data.
// Returns nil if the container does not have the required labels.
func serverFromInspect(c *containerInspect) *Server {
	labels := c.Config.Labels
	if labels["mcp.enable"] != "true" {
		return nil
	}

	name := labels["mcp.name"]
	if name == "" {
		// Use first 12 chars of container ID, or the full ID if shorter.
		end := 12
		if end > len(c.ID) {
			end = len(c.ID)
		}
		name = c.ID[:end]
	}

	transport := Transport(labels["mcp.transport"])
	if transport == "" {
		transport = defaultTransport
	}

	port := labels["mcp.port"]
	if port == "" {
		port = defaultPort
	}

	path := labels["mcp.path"]
	if path == "" {
		path = defaultPath
	}

	// Resolve the host port mapping if available.
	hostPort := resolveHostPort(c, port)

	return &Server{
		ContainerID: c.ID,
		Name:        name,
		Transport:   transport,
		Address:     fmt.Sprintf("127.0.0.1:%s", hostPort),
		Path:        path,
	}
}

// resolveHostPort returns the host-mapped port for containerPort, or containerPort
// if no mapping is found.
func resolveHostPort(c *containerInspect, containerPort string) string {
	key := fmt.Sprintf("%s/tcp", containerPort)
	if bindings, ok := c.NetworkSettings.Ports[key]; ok && len(bindings) > 0 {
		if p := bindings[0].HostPort; p != "" {
			return p
		}
	}
	return containerPort
}

// Servers returns a snapshot of the currently discovered MCP servers.
func (g *Gateway) Servers() []*Server {
	g.mu.RLock()
	defer g.mu.RUnlock()

	result := make([]*Server, 0, len(g.servers))
	for _, s := range g.servers {
		result = append(result, s)
	}
	return result
}

// ServerByName returns the first Server with the given name, or nil if none.
func (g *Gateway) ServerByName(name string) *Server {
	g.mu.RLock()
	defer g.mu.RUnlock()
	for _, s := range g.servers {
		if s.Name == name {
			return s
		}
	}
	return nil
}

// ToolEndpoint returns the HTTP endpoint URL for a named MCP server's tools API.
func (g *Gateway) ToolEndpoint(serverName string) (string, error) {
	srv := g.ServerByName(serverName)
	if srv == nil {
		return "", fmt.Errorf("gateway: server %q not found", serverName)
	}
	if srv.Transport != TransportHTTP {
		return "", fmt.Errorf("gateway: server %q uses %s transport (only http supported for endpoint routing)",
			serverName, srv.Transport)
	}
	return fmt.Sprintf("http://%s%s", srv.Address, srv.Path), nil
}

// Health checks whether the Docker daemon is reachable.
func (g *Gateway) Health(ctx context.Context) error {
	if _, err := g.docker.Run(ctx, "info", "--format", "{{.ServerVersion}}"); err != nil {
		return fmt.Errorf("gateway: docker daemon unreachable: %w", err)
	}
	return nil
}

// StartDiscovery starts a background goroutine that periodically re-runs
// Discover to keep the server registry up to date. It runs until ctx is
// cancelled.
func (g *Gateway) StartDiscovery(ctx context.Context) {
	ticker := time.NewTicker(discoveryInterval)
	defer ticker.Stop()
	// Run once immediately before waiting for the first tick.
	_, _ = g.Discover(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _ = g.Discover(ctx)
		}
	}
}

// parseLines splits Docker CLI line-delimited output into non-empty strings.
func parseLines(b []byte) []string {
	var lines []string
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}
