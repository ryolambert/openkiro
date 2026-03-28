package sandbox

// AgentConfig returns a Config preset for running general agent workloads
// (rtk, icm, headroom, mcp-gateway) inside a sandbox container.
//
// Unlike the strict default isolation (NetworkMode "none"), agent containers
// use bridge networking so they can reach the openkiro proxy and external MCP
// servers. All other security constraints (non-root, read-only root FS,
// capability dropping) are preserved.
func AgentConfig() Config {
	cfg := DefaultConfig()
	cfg.NetworkMode = "bridge"
	return cfg
}

// ClaudeCodeConfig returns a Config preset for running Claude Code inside a
// sandbox container. Claude Code connects to the openkiro proxy at
// ANTHROPIC_BASE_URL (default http://127.0.0.1:1234) and requires outbound
// network access to reach AWS CodeWhisperer via the proxy.
//
// Environment variables set by this preset:
//
//	ANTHROPIC_BASE_URL     — points to the openkiro proxy inside the container
//	ANTHROPIC_API_KEY      — set to a placeholder (openkiro handles auth)
//	NODE_NO_WARNINGS       — suppresses Node.js startup noise
//	DISABLE_AUTOUPDATER    — prevents Claude Code auto-update inside a sandbox
func ClaudeCodeConfig() Config {
	cfg := AgentConfig()
	cfg.Image = AgentImageName
	cfg.Env = append(cfg.Env,
		"ANTHROPIC_BASE_URL=http://127.0.0.1:1234",
		"ANTHROPIC_API_KEY=openkiro-proxy",
		"NODE_NO_WARNINGS=1",
		"DISABLE_AUTOUPDATER=1",
	)
	return cfg
}

// KiroConfig returns a Config preset for running Kiro-based agent workloads.
// Kiro uses AWS CodeWhisperer for inference; the openkiro proxy handles the
// token refresh and request translation automatically.
//
// Environment variables set by this preset:
//
//	ANTHROPIC_BASE_URL  — points to the openkiro proxy
//	ANTHROPIC_API_KEY   — placeholder (openkiro handles auth via AWS SSO)
//	KIRO_PROXY          — marks the proxy as openkiro for Kiro tooling
func KiroConfig() Config {
	cfg := AgentConfig()
	cfg.Image = AgentImageName
	cfg.Env = append(cfg.Env,
		"ANTHROPIC_BASE_URL=http://127.0.0.1:1234",
		"ANTHROPIC_API_KEY=openkiro-proxy",
		"KIRO_PROXY=openkiro",
	)
	return cfg
}

// AgentImageName is the canonical Docker image tag for the agent sandbox.
const AgentImageName = "openkiro-sandbox:latest"
