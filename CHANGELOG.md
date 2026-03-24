# Changelog

All notable changes to this project will be documented in this file.

This project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html)
and uses [Conventional Commits](https://www.conventionalcommits.org/) for commit messages.

## Version Scheme

- `MAJOR` — breaking API or CLI changes
- `MINOR` — new features, backward-compatible
- `PATCH` — bug fixes, performance improvements

## [Unreleased]

### Added
- Anthropic-shaped local proxy for AWS CodeWhisperer
- Token reading from `~/.aws/sso/cache/kiro-auth-token.json`
- Token refresh with automatic retry
- Streaming SSE support with incremental frame parsing
- `POST /v1/messages`, `GET /v1/models`, `GET /health` endpoints
- `openkiro server` — start proxy in foreground
- `openkiro start/stop/status` — daemon mode (macOS launchd, Linux PID)
- `openkiro claude` — configure `~/.claude.json` for proxy
- `openkiro export` — print shell-ready `ANTHROPIC_*` env vars
- `openkiro version` — build-time version info
- Connection pooling with shared `http.Client`
- Deterministic `/v1/models` response ordering
- Security hardening: localhost-only binding, request body limits, token redaction
- Platform-aware logging (`~/Library/Logs/openkiro/`, `~/.local/state/openkiro/`)
- Cross-platform builds (linux/darwin/windows × amd64/arm64)
- GoReleaser automation with nFPM packages (deb/rpm/apk/archlinux)
- CI pipeline with matrix testing, linting, and release gating
- Conventional commit changelog generation

[Unreleased]: https://github.com/ryolambert/openkiro/commits/main
