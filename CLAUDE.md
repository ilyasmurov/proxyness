# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Proxyness — proxy system with a Go server (VPS in Netherlands), Go daemon (local SOCKS5 + TUN tunnel), privileged helper (TUN device management), and Electron desktop client. Single TLS port (443) multiplexes proxy traffic and HTTP admin panel.

**Landing page**: https://proxyness.smurov.com

## Build & Test Commands

```bash
# Run all Go tests (pkg, daemon, server, test)
make test

# Build server (Linux amd64, output: dist/proxy-server)
make build-server

# Build daemon (macOS arm64/amd64 + Windows, output: dist/daemon-*)
make build-daemon

# Build helper (macOS arm64/amd64 + Windows, output: dist/helper-*)
cd helper && go build -o ../dist/helper-darwin-arm64 ./cmd/

# Build Electron client (bundles daemon+helper into resources, builds PKG/exe)
make build-client

# Run Electron in dev mode
make dev

# Run a single Go test
cd server && go test ./internal/db/ -run TestDeviceCRUD -v

# Clean build artifacts
make clean
```

## Protocol Flow

```
                    ┌─ Browsers ──→ System SOCKS5 proxy (:1080) ─┐
Client App ─────────┤                                             ├─→ TLS → Server (:443)
                    └─ Apps ──→ TUN device ──→ Helper relay ──→   │
                                  Daemon (gVisor netstack) ───────┘
                                                                    ↓
                                                              Peek msg type
                                                              0x01 → TCP relay
                                                              0x02 → UDP relay
                                                              HTTP → Admin panel
```

## Extended Documentation

The detailed sections live in their own files and are loaded into Claude's context via `@` imports:

- **@docs/claude/architecture.md** — module layout: Server, Daemon (TUN engine, SOCKS5), Helper, Client (Electron), Admin Dashboard, Browser Extension, Config Service.
- **@docs/claude/deploy.md** — tag-triggered deploys, single-VPS Aeza topology (nginx SNI router, Postgres).
- **@docs/claude/decisions.md** — load-bearing design decisions and gotchas: UDP transport, device-key cache, health-loop detectors (D1–D4), ENETUNREACH slow-poll recovery, UI amber/muted rule, and more. Read these before touching the matching code paths.

## MCP Tools: code-review-graph

**IMPORTANT: This project has a knowledge graph. ALWAYS use the
code-review-graph MCP tools BEFORE using Grep/Glob/Read to explore
the codebase.** The graph is faster, cheaper (fewer tokens), and gives
you structural context (callers, dependents, test coverage) that file
scanning cannot.

### When to use graph tools FIRST

- **Exploring code**: `semantic_search_nodes` or `query_graph` instead of Grep
- **Understanding impact**: `get_impact_radius` instead of manually tracing imports
- **Code review**: `detect_changes` + `get_review_context` instead of reading entire files
- **Finding relationships**: `query_graph` with callers_of/callees_of/imports_of/tests_for
- **Architecture questions**: `get_architecture_overview` + `list_communities`

Fall back to Grep/Glob/Read **only** when the graph doesn't cover what you need.

### Key Tools

| Tool | Use when |
|------|----------|
| `detect_changes` | Reviewing code changes — gives risk-scored analysis |
| `get_review_context` | Need source snippets for review — token-efficient |
| `get_impact_radius` | Understanding blast radius of a change |
| `get_affected_flows` | Finding which execution paths are impacted |
| `query_graph` | Tracing callers, callees, imports, tests, dependencies |
| `semantic_search_nodes` | Finding functions/classes by name or keyword |
| `get_architecture_overview` | Understanding high-level codebase structure |
| `refactor_tool` | Planning renames, finding dead code |

### Workflow

1. The graph auto-updates on file changes (via hooks).
2. Use `detect_changes` for code review.
3. Use `get_affected_flows` to understand impact.
4. Use `query_graph` pattern="tests_for" to check coverage.
