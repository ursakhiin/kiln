# Problem Statement

As a user, working with an AI agent to plan projects is mentally taxing. Markdown files are ideal for an agent to work from, but they make dependencies, priorities, and history difficult for a user to track in their head.

Underdraw is an agentic collaboration tool that facilitates ideation through implementation for users building with agentic workflows.

# Use Cases

As a user, I want to either use a GUI to jot down ideas or collaborate with an agent to ideate and have the agent document the ideas in Underdraw.

As a user, I want to create a rough idea for a project/feature and then collaborate with an agent on expanding it.

As a user, I want to view dependencies in a visual workstream/flow chart and prioritize features easily with drag-and-drop.

As a user, I want to review work an agent has finished before it is considered done.

As an agent, I want to implement from Markdown, as that works best for me, but I need to ensure the work I'm doing matches changes made in the user's interface.

As an agent, I want to ask Underdraw for the next task to work on and claim it so other agents don't duplicate my work.

# Data Model

* **Project → Feature → Task.** A single repo can contain multiple projects.
* **Ideas are draft Features**, not a separate entity. An agent never implements a draft.
* **Priority** is a single ranked order of features across all projects in the repo, set by drag-and-drop. Tasks are ranked only within their feature.
* **Dependencies** can link any feature or task to any other, including across projects. Underdraw rejects any dependency that would create a cycle.
  * Each dependency says when it stops blocking: when the blocking item is `done` (the default) or once it reaches `in review`.
* **Lifecycle:** `draft → ready → in progress → in review → done`
  * An agent may move work up to `in review`; only the user moves it to `done`.
  * A Feature's status is derived from its Tasks (e.g. `in progress` once any task starts, `in review` when all tasks are in review), and the user can override it.
* **History** covers:
  * An append-only change log of every change and who made it (user or a specific agent)
  * Versions of each item's content, which can be diffed and restored
  * Decision notes attached to items, recording why decisions were made

# Agent Interaction

* Agents interact with Underdraw only through its MCP server. They never write Underdraw's data files directly.
* Markdown views are **read-only** and **returned by MCP tools** (e.g. a task with its parent feature context). Underdraw does not write markdown files to disk.
* `next_task` returns a `ready` task that isn't blocked or claimed, ordered by feature rank and then task rank, with **feature affinity**:
  * An agent prefers the next task in the feature it is working on.
  * Other agents prefer features no agent is working on, which spreads agents out and reduces collisions in the same code.
* **Task claiming:** an agent claims a task when it starts. Claimed tasks are skipped by `next_task`.
  * A claim renews whenever its agent calls Underdraw, and expires if the agent stops.
  * An expired claim leaves the task `in progress` but unclaimed. `next_task` offers such tasks first, and their context warns that another agent may have left partial work.
* **Work state:** agents record what another agent would need to continue a task (`update_work_state`).
  * Typed fields: branch, worktree path, base and latest commits, PR URL, agent session (host, session ID, transcript path, model), progress notes, and related files.
  * Credentials are recorded only by name, never by value. Underdraw rejects values that look like secrets, because work state is committed with the task.
  * A key-value map holds anything else.
  * Agents update it when they start, after each commit, and before they stop. A follow-up agent that picks up an abandoned task gets it in the task's context.
  * Underdraw fills in the agent host and version from the MCP connection.
* Only the user changes rankings or deletes items. Agents can't promote draft features, override a feature's status, or mark work `done`.
* **Staleness:**
  * Every item has a revision number, and each markdown view includes it.
  * An MCP write based on an outdated revision is rejected with a description of what changed. A `get_item_changes(id, since_revision)` tool lets the agent catch up.
  * Underdraw also sends MCP resource-update notifications to clients that subscribe. This is optional; the revision check is the guarantee.

# Architecture

* **Local-first, single user plus multiple agents.** No auth in v1.
* **Storage:**
  * Text files under `.underdraw/` (one file per item), committed to git, are the source of truth.
  * History is append-only, with one file per entry under `.underdraw/history/<item-id>/`, so branches that both add history merge cleanly.
  * Work-state updates rewrite the task file without incrementing its revision or adding history. Each agent session's final work state is recorded once: in the task's next history entry, or on its own when the claim ends.
  * A gitignored SQLite index is rebuilt from those files for fast queries and graph rendering.
* **Daemon:** one `underdraw serve` process per repo owns the files and the index. It serves the web GUI, the CLI's API and MCP (streamable HTTP).
  * Any `underdraw` CLI command auto-starts the daemon if it isn't running.
  * The daemon watches `.underdraw/` for outside changes (hand edits, `git pull`, branch switches). It rebuilds the index, bumps revisions and pushes updates. Invalid files are reported without crashing the daemon.
* **Schema:** Protocol Buffers are the single source of truth for the data model and API. The following are generated from them:
  * MCP tool schemas (via a custom `protoc` plugin)
  * The Connect API used by the GUI and CLI
  * The storage format
  * This is adopted early, by choice, to make future expansion easier.

# Interfaces

* **Web GUI**, served locally by the daemon:
  * List view with drag-and-drop ranking
  * Dependency graph (flow chart) view
* **CLI:** scriptable commands (e.g. `underdraw list`, `underdraw add`, `underdraw next`). A full-screen TUI comes later.

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

## Build Order

Agent loop first, so the agent workflow can be tested as early as possible:

1. Data model, `.underdraw/` file storage and revisions
2. MCP server (create/read items, `next_task`, claiming, status updates, markdown views)
3. CLI commands
4. Web GUI list view with drag-and-drop ranking
5. Dependency graph view
6. History extras (version diff/restore) and MCP push notifications

**Dogfooding:** once step 2 works, move these requirements into Underdraw items and plan the rest of Underdraw's build in Underdraw.

# Non-Goals (v1)

* Auth, hosted deployment or multi-user teams
* Sync with external trackers (GitHub Issues, Jira, Linear, etc.)
* Launching or orchestrating agents; Underdraw only serves the agents that connect to it
* Time tracking, estimates, due dates or sprints
* Two-way markdown sync; markdown views are read-only
* Mobile apps or a desktop wrapper; desktop browser only
* Code awareness: reading the repo's code, linking commits or verifying that tasks were done
* Plugins, extensibility or custom fields

# Open Questions

* Success criteria (to be defined after dogfooding begins)
