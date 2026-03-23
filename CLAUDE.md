# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

This is a Go CLI tool called `kirolink` that manages Kiro authentication tokens and provides an Anthropic API proxy service. The tool acts as a bridge between Anthropic API requests and AWS CodeWhisperer, translating requests and responses between the two formats.

## Build and Development Commands

```bash
# Build the application
go build -o kirolink kirolink.go

# Run tests
go test ./...

# Run specific test in protocol package
go test ./protocol -v

# Run the application
./kirolink [command]
```

## Application Commands

- `./kirolink read` - Read and display token information
- `./kirolink refresh` - Refresh the access token using refresh token
- `./kirolink export` - Export environment variables for other tools
- `./kirolink server [port]` - Start HTTP proxy server (default port 8080)

## Architecture

### Core Components

1. **Token Management** (`kirolink.go`)
   - Reads tokens from `~/.aws/sso/cache/kiro-auth-token.json`
   - Handles token refresh via Kiro auth service
   - Cross-platform environment variable export

2. **API Translation** (`kirolink.go`)
   - Converts Anthropic API requests to CodeWhisperer format
   - Maps model names via `ModelMap` (line 218-221)
   - Handles conversation history and system messages

3. **HTTP Proxy Server** (`kirolink.go`)
   - Serves on `/v1/messages` endpoint
   - Supports both streaming and non-streaming requests
   - Automatic token refresh on 403 errors

4. **Response Parser** (`protocol/sse_parser.go`)
   - Parses binary CodeWhisperer responses
   - Converts to Anthropic-compatible SSE events
   - Handles tool use and text content blocks

### Key Data Structures

- `AnthropicRequest` - Incoming API requests
- `CodeWhispererRequest` - Outgoing AWS requests  
- `TokenData` - Authentication token storage
- `SSEEvent` - Streaming response events

### Request Flow

1. Client sends Anthropic API request to `/v1/messages`
2. Server reads token from filesystem
3. Request converted to CodeWhisperer format
4. Proxied to AWS CodeWhisperer API
5. Response parsed and converted back to Anthropic format
6. Streamed or returned as JSON to client

## Development Notes

- Uses hardcoded proxy `127.0.0.1:9000` for AWS requests
- Model mapping required between Anthropic and CodeWhisperer model IDs
- Response files saved as `msg_[timestamp]response.raw` for debugging
- Automatic token refresh on authentication failures