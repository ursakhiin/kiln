# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project

**kiln**: "Turn your rough ideas into an Agentic project." A local-first tool where users and AI agents plan and track Projects → Features → Tasks together. Licensed under Apache 2.0.

`Requirements.md` is the authoritative spec. Read it before designing or implementing anything.

## Current state

No code has been written yet, so there are no build, lint or test commands. Add them here once the toolchain exists.

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
