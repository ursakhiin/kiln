# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project

**kiln**: "Turn your rough ideas into an Agentic project." A local-first tool where users and AI agents plan and track Projects → Features → Tasks together. Licensed under Apache 2.0.

`Requirements.md` is the authoritative spec. Read it before designing or implementing anything.

## Commands

- `make build`: build the web GUI into `internal/webui/dist/`, then the Go binary into `bin/kiln`
- `make test`: `go test ./...`. For a single test: `go test ./internal/core -run TestName`
- `make lint`: `buf lint`, `go vet` and the web linter (oxlint)
- `make gen`: regenerate code from `proto/` into `gen/go/` and `web/src/gen/` (both committed)
- `cd web && npm run dev`: run the Vite dev server for the GUI

`buf`, `protoc-gen-go` and `protoc-gen-connect-go` are pinned as Go tools in `go.mod` and run with `go tool <name>`; no global install is needed.

## Layout

- **CLI:** `cmd/kiln/` was generated with `cobra-cli`; commands live in `cmd/kiln/cmd/`. To add one, run `cobra-cli add <name> -a "Peter Rauhut" -l none` from `cmd/kiln/` (cobra-cli is in `~/go/bin`).
- **`internal/`:** each package's `doc.go` states its responsibility. `api/` and `mcp/` are thin adapters over `service/`, and `core/` holds the domain rules with no I/O.
- **`cmd/protoc-gen-kiln-mcp/`:** a custom `protoc` plugin that will generate MCP tools from annotated RPCs. Once it's implemented, enable it in `buf.gen.yaml`.

## Planned architecture (from Requirements.md)

- **Stack:** Go backend built as a single binary, with a React + React Flow GUI embedded.
- **Daemon:** one `kiln serve` daemon per repo is the **only writer** of Kiln data. The web GUI, the CLI and MCP (streamable HTTP) all go through it. CLI commands auto-start it.
- **Storage:**
  - Text files under `.kiln/` (one per item) are the source of truth and are committed to git.
  - The SQLite index is a gitignored cache that must always be rebuildable from those files.
  - The daemon watches `.kiln/` and reloads on outside changes.
- **Protobuf is the single schema.** The MCP tool schemas (via a custom `protoc` plugin), the Connect API and the storage format are all generated from `.proto` files. Change the `.proto` first; don't hand-edit generated code.
- **Agents use MCP only.** Markdown views are read-only and are returned by MCP tools, never written to disk.
- **Concurrency rules:**
  - Every item has a revision; writes based on an outdated revision are rejected.
  - Agents claim tasks before working on them.
  - Agents can move work up to `in review`; only the user sets `done`.
