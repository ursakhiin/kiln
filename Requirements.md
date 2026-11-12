# Problem Statement

As a user, working with an AI agent to plan projects is mentally taxing. Markdown files are ideal for an agent to work from, but they make dependencies, priorities, and history difficult for a user to track in their head.

Kiln is an agentic collaboration tool that facilitates ideation through implementation for users building with agentic workflows.

# Use Cases

As a user, I want to either use a GUI to jot down ideas or collaborate with an agent to ideate and have the agent document the ideas in Kiln.

As a user, I want to create a rough idea for a project/feature and then collaborate with an agent on expanding it.

As a user, I want to view dependencies in a visual workstream/flow chart and prioritize features easily with drag-and-drop.

As a user, I want to review work an agent has finished before it is considered done.

As an agent, I want to implement from Markdown, as that works best for me, but I need to ensure the work I'm doing matches changes made in the user's interface.

As an agent, I want to ask Kiln for the next task to work on and claim it so other agents don't duplicate my work.

# Data Model

* **Project → Feature → Task.** A single repo can contain multiple projects.
* **Ideas are draft Features**, not a separate entity. An agent never implements a draft.
* **Priority** is a single ranked order across all projects in the repo, set by drag-and-drop.
* **Dependencies** can link any feature or task to any other, including across projects. Kiln rejects any dependency that would create a cycle.
* **Lifecycle:** `draft → ready → in progress → in review → done`
  * An agent may move work up to `in review`; only the user moves it to `done`.
  * A Feature's status is derived from its Tasks (e.g. `in progress` once any task starts, `in review` when all tasks are in review), and the user can override it.
* **History** covers:
  * An append-only change log of every change and who made it (user or a specific agent)
  * Versions of each item's content, which can be diffed and restored
  * Decision notes attached to items, recording why decisions were made

# Agent Interaction

* Agents interact with Kiln only through its MCP server. They never write Kiln's data files directly.
* Markdown views are **read-only** and **returned by MCP tools** (e.g. a task with its parent feature context). Kiln does not write markdown files to disk.
* `next_task` returns the highest-ranked `ready` task that isn't blocked or claimed.
* **Task claiming:** an agent claims a task when it starts. Claimed tasks are skipped by `next_task`, and claims expire if the agent stops responding.
* **Staleness:**
  * Every item has a revision number, and each markdown view includes it.
  * An MCP write based on an outdated revision is rejected with a description of what changed. A `changes_since(revision)` tool lets the agent catch up.
  * Kiln also sends MCP resource-update notifications to clients that subscribe. This is optional; the revision check is the guarantee.

# Architecture

* **Local-first, single user plus multiple agents.** No auth in v1.
* **Storage:**
  * Text files under `.kiln/` (one file per item), committed to git, are the source of truth.
  * A gitignored SQLite index is rebuilt from those files for fast queries and graph rendering.
* **Daemon:** one `kiln serve` process per repo owns the files and the index. It serves the web GUI, the CLI's API and MCP (streamable HTTP).
  * Any `kiln` CLI command auto-starts the daemon if it isn't running.
  * The daemon watches `.kiln/` for outside changes (hand edits, `git pull`, branch switches). It rebuilds the index, bumps revisions and pushes updates. Invalid files are reported without crashing the daemon.
* **Schema:** Protocol Buffers are the single source of truth for the data model and API. The following are generated from them:
  * MCP tool schemas (via a custom `protoc` plugin)
  * The Connect API used by the GUI and CLI
  * The storage format
  * This is adopted early, by choice, to make future expansion easier.

# Interfaces

* **Web GUI**, served locally by the daemon:
  * List view with drag-and-drop ranking
  * Dependency graph (flow chart) view
* **CLI:** scriptable commands (e.g. `kiln list`, `kiln add`, `kiln next`). A full-screen TUI comes later.

# Tech Stack

* **Backend:** Go, as a single binary with the GUI embedded
  * MCP: official Go SDK
  * API: Connect (`connect-go`)
  * Protobuf tooling: `buf`
  * Index: SQLite via `modernc.org/sqlite` (pure Go, no C compiler needed)
  * File watching: `fsnotify`
  * CLI: `cobra`
  * TUI (later): Bubble Tea
* **Frontend:** React + React Flow, calling the API through `connect-es`

# MVP Scope

v1 includes:

* MCP server and the full data model (ranking, dependencies, revisions, history, claiming)
* CLI commands
* Web GUI list view with drag-and-drop ranking
* Web GUI dependency graph

# Open Questions

* Build order within the MVP (e.g. MCP + data model → CLI → GUI list → graph)
* Success criteria
* Non-goals
