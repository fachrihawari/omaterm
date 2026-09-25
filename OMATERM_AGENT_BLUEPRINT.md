# OmaTerm
## Product & Technical Architecture Specification
### Agent Handoff / Implementation Blueprint

**Project:** OmaTerm  
**Primary target:** Linux, with Omarchy as the first-class environment  
**Future target:** Cross-platform desktop support, at minimum Linux and later macOS/Windows where practical  
**Primary language:** Rust  
**Primary UI framework:** GPUI  
**Initial terminal engine:** `alacritty_terminal`, behind an OmaTerm-owned abstraction  
**Primary inspiration:** Kero (`egoist/kero`)  
**Product identity:** Terminal-first developer workspace with first-class CLI automation and an architecture intentionally designed for future coding-agent control.

---

# 1. Purpose of This Document

This document is the authoritative starting specification for building **OmaTerm**.

It compiles the product ideas, architectural analysis, Kero observations, technology decisions, constraints, and development strategy agreed upon before implementation begins.

An implementation agent should treat the decisions in this document as the current default architecture. Do not repeatedly reopen framework or product decisions unless implementation evidence shows a material technical blocker.

Where this document says **MUST**, **SHOULD**, or **MUST NOT**, interpret those terms literally:

- **MUST** — required for architectural correctness.
- **SHOULD** — preferred unless there is a concrete technical reason not to do it.
- **MUST NOT** — avoid because it conflicts with the intended architecture or product scope.

The goal of the first implementation is not to clone Kero pixel-for-pixel.

The goal is to preserve the strongest ideas behind Kero while creating a Linux-first terminal workspace whose internal command model is suitable for future AI-agent automation.

---

# 2. Project Name

The project is called:

# **OmaTerm**

The naming is intentional:

- **Oma** → Omarchy
- **Term** → Terminal

The display/product name should be:

```text
OmaTerm
```

The CLI executable should be:

```text
omaterm
```

Use `omaterm` consistently for technical identifiers unless a platform convention requires another form.

Recommended Linux paths:

```text
~/.config/omaterm/config.toml
$XDG_STATE_HOME/omaterm/
$XDG_CACHE_HOME/omaterm/
$XDG_RUNTIME_DIR/omaterm.sock
```

Recommended environment-variable prefix:

```text
OMATERM_
```

Examples:

```text
OMATERM_PROJECT_ID
OMATERM_TAB_ID
OMATERM_PANE_ID
OMATERM_SESSION_ID
OMATERM_SOCKET
OMATERM_TOKEN
```

---

# 3. Product Vision

OmaTerm is a **terminal-centered developer workspace**.

It is not intended to become a traditional IDE.

The basic product idea is:

> Keep terminals, projects, tabs, split panes, lightweight repository context, and eventually coding-agent coordination in one fast desktop workspace, while keeping the terminal as the primary place where work happens.

The user should spend most of their time inside terminal panes.

Supporting interfaces exist to improve terminal workflows rather than replacing them.

A conceptual future OmaTerm workspace may look like:

```text
┌──────────────────────────────────────────────────────────────┐
│ OmaTerm                                         main +24 -3  │
├──────────────┬───────────────────────────────────────────────┤
│ Projects     │ api                          │ tests          │
│              │                              │                │
│ my-app       │ $ codex                      │ $ cargo test   │
│ backend      │                              │                │
│ otaqku       │ Working...                   │ PASS           │
│ fortis       │                              │                │
│              ├──────────────────────────────┴────────────────┤
│              │ $ pnpm dev                                   │
│              │ Server listening on :3000                    │
│              │                                               │
└──────────────┴───────────────────────────────────────────────┘
```

The terminal remains the center of gravity.

---

# 4. Primary User

The initial user is a technically experienced developer who:

- works heavily in terminal environments;
- uses Linux, especially Omarchy;
- works across multiple repositories;
- runs multiple shells/processes simultaneously;
- uses coding agents such as Codex, Claude Code, Gemini CLI, OpenCode, Aider, or similar tools;
- wants faster switching between related terminal jobs;
- may eventually want one coding agent to control or coordinate other terminal panes;
- values keyboard-driven workflows;
- wants a lightweight workspace rather than a full IDE.

OmaTerm should feel appropriate for advanced users without requiring them to learn a complicated proprietary shell environment.

---

# 5. Product Principles

OmaTerm should follow these principles.

## 5.1 Terminal First

The terminal is not a plugin embedded in an editor.

The terminal is the primary workspace.

Other capabilities exist around it.

## 5.2 Real Shells

OmaTerm MUST launch real PTYs and real user shells.

It MUST NOT emulate shell commands itself.

The user's existing shell configuration should continue to work:

```text
bash
zsh
fish
starship
aliases
functions
environment
dotfiles
TUI applications
```

## 5.3 Fast and Native-Feeling

OmaTerm should prioritize:

- keyboard responsiveness;
- efficient rendering;
- low idle CPU usage;
- predictable memory behavior;
- fast pane switching;
- correct IME/input handling;
- Wayland compatibility.

## 5.4 Inspectable Automation

Future automation must use semantic commands.

Agents should say, conceptually:

```text
split pane right
run cargo test
read pane output
wait for text
```

They should NOT simulate keyboard shortcuts or mouse actions.

## 5.5 Human Control

Automation should never silently turn permission, trust, credential, or destructive prompts into approval.

Agent completion is not equivalent to user approval.

## 5.6 Project-Scoped Context

A terminal/agent operating inside Project A should not automatically gain control over Project B.

Project boundaries should become capability/security boundaries.

## 5.7 Avoid IDE Creep

Do not casually add:

- LSP;
- debugging;
- heavy semantic indexing;
- refactoring engines;
- extension marketplaces;
- cloud accounts;
- collaborative editing.

These may be evaluated in the future, but they are not part of OmaTerm's defining product.

---

# 6. What We Learned From Kero

Kero is the primary product reference for OmaTerm.

Kero should be treated as a behavioral and architectural reference, not as a source codebase to copy.

Its strongest ideas are listed below.

## 6.1 Kero Is a Terminal Workspace, Not an IDE

Kero's own product philosophy keeps the terminal primary.

Files, Git, diffs, browser panes, process information, and coding-agent state stay close to terminal sessions so a developer can supervise work without continually switching applications.

OmaTerm should preserve this philosophy.

## 6.2 Project → Tab → Pane Tree

One of Kero's best architectural decisions is its workspace hierarchy:

```text
Application
└── Window
    └── Projects[]
        └── Tabs[]
            └── Pane Tree
                └── Pane Content
```

OmaTerm should use the same conceptual hierarchy.

## 6.3 Recursive Pane Layout

Kero represents pane layouts as a recursive binary tree rather than as a fixed row/column grid.

Conceptually:

```text
PaneNode
├── Pane
└── Split
    ├── axis
    ├── fraction
    ├── first: PaneNode
    └── second: PaneNode
```

Example:

```text
                horizontal
              /            \
         terminal          vertical
                          /        \
                     terminal    terminal
```

Benefits:

- nested splits;
- splitting only the focused pane;
- local resize behavior;
- pane removal with parent collapse;
- easy persistence;
- pane drag/rearrangement later;
- pane zoom later;
- equalization later.

OmaTerm MUST use a recursive pane tree or an equivalently expressive model.

A flat matrix should not be the source of truth.

## 6.4 Long-Lived Terminal Sessions

Kero separates terminal session lifetime from UI mounting.

This is critical.

Changing tabs or pane layout should NOT recreate the terminal.

PTY state, process state, scrollback, selection, and terminal parser state must belong to a durable `TerminalSession`, not a transient UI component.

OmaTerm MUST follow this rule:

```text
terminal process lifecycle != terminal view lifecycle
```

## 6.5 Terminal Backend Abstraction

Kero hides Ghostty and Alacritty behind its own terminal-surface abstraction.

This allows the rest of the application to talk in Kero's vocabulary rather than emulator-specific types.

OmaTerm should copy this architectural idea.

The first engine may be Alacritty, but the core should depend on an OmaTerm interface.

## 6.6 Persist Layout, Not Running Processes

When Kero restarts, it restores projects/tabs/pane layouts but launches fresh shells.

This is a sensible default.

OmaTerm should persist:

- projects;
- tabs;
- recursive pane layout;
- focused pane;
- working directories;
- selected project/tab;
- custom names;
- UI layout state.

OmaTerm should NOT pretend a process survived application shutdown.

Optional static scrollback restoration may be added separately.

## 6.7 Agent State Must Be Semantic

Kero evolved away from guessing coding-agent completion by scraping rendered terminal text.

OmaTerm should not implement logic like:

```text
if terminal_text contains "Done":
    agent = finished
```

Future agent integrations should prefer:

1. provider lifecycle events;
2. process identity;
3. explicit OmaTerm commands/events.

Rendered text is useful for user-visible output and bounded automation reads, but should not be the authority for semantic agent lifecycle.

## 6.8 Project-Scoped Agent Automation

Kero's automation is constrained to the current project.

OmaTerm should preserve that security model.

## 6.9 CLI/Local Automation Is a First-Class Architecture

Kero exposes local project-aware automation through commands and an authenticated local communication mechanism.

OmaTerm should go even further and design the command system before agent features arrive.

---

# 7. Current OmaTerm Scope Decisions

The following decisions are intentionally different from full Kero parity.

## 7.1 Browser Panes Are Not Required

OmaTerm does NOT need browser panes in the current plan.

This removes one of the major reasons to use Qt WebEngine.

It also makes an all-Rust architecture much more attractive.

Browser panes MAY be reconsidered in the future.

They MUST NOT influence the first architecture.

## 7.2 Linux Is the First-Class Platform

OmaTerm should initially optimize for:

```text
Linux
└── Omarchy
    └── Wayland
```

X11 support is desirable because GPUI supports it and it improves Linux compatibility.

macOS and Windows are future targets.

Do not compromise the Linux experience solely to achieve immediate lowest-common-denominator portability.

## 7.3 CLI Control Is a Core Requirement

The `omaterm` CLI is not merely a convenience launcher.

It is the future automation surface.

Important workspace operations should eventually have semantic CLI equivalents.

## 7.4 Agent Control Is Future Scope, Architecture Is Present Scope

The first release does not need full multi-agent orchestration.

However, the architecture MUST make it possible to add later without redesigning terminal ownership, panes, project identity, or IPC.

---

# 8. UI Framework Decision

## 8.1 Primary Choice: GPUI

OmaTerm should start with:

```text
Rust + GPUI
```

GPUI is a GPU-accelerated Rust UI framework developed as part of Zed.

Current GPUI documentation explicitly supports:

- Linux/FreeBSD with Wayland;
- Linux/FreeBSD with X11;
- macOS;
- Windows.

For Linux, relevant GPUI platform features include:

```toml
gpui_platform = {
  version = "*",
  features = ["wayland", "x11"]
}
```

GPUI is still pre-1.0 and can introduce breaking changes.

This is an accepted project risk.

### Why GPUI Fits OmaTerm

OmaTerm's UI workload is dominated by:

- text rendering;
- keyboard input;
- terminal cell updates;
- scrolling;
- split panes;
- command palette interactions;
- selection;
- highly dynamic developer tooling UI.

GPUI was built for a high-performance developer application.

Zed itself provides evidence that GPUI can support:

```text
GPUI
+
multiple terminals
+
alacritty_terminal
+
Linux
+
Wayland/X11
+
high-frequency text UI
```

This is unusually close to OmaTerm's required workload.

## 8.2 Alternative Frameworks

If GPUI presents a material blocker, evaluate alternatives in this order:

### Iced

Pros:

- Rust;
- cross-platform;
- GPU rendering;
- established ecosystem.

Cons:

- less direct evidence for a Zed/Kero-style high-complexity developer workspace.

### Slint

Pros:

- mature cross-platform UI;
- GPU renderers;
- desktop support.

Cons:

- separate UI DSL;
- less attractive for an architecture intended to remain predominantly Rust.

### egui

Pros:

- simple;
- excellent for developer tooling;
- fast iteration.

Cons:

- significant custom work may be required to achieve polished terminal-product UX.

### Tauri

Good for fast prototypes, especially with xterm.js.

However, a long-term OmaTerm implementation should not default to browser/WebView terminal rendering unless GPUI/native rendering proves impractical.

### Qt

Qt remains technically capable.

It is no longer the preferred option because OmaTerm currently does not require embedded browser panes.

---

# 9. Terminal Engine Decision

## 9.1 Initial Recommendation

Start with:

```text
alacritty_terminal
```

as the terminal state/emulation engine.

Primary reasons:

- Rust-native integration;
- proven use inside Zed;
- easier conceptual reference for a GPUI terminal;
- no language boundary required for core emulation.

Zed currently maintains separate terminal and terminal-view crates and its terminal crate depends on `alacritty_terminal`.

This validates the general architecture.

## 9.2 Do Not Couple OmaTerm to Alacritty

Create an OmaTerm abstraction immediately.

Conceptually:

```rust
pub trait TerminalEngine {
    fn resize(&mut self, cols: u16, rows: u16);
    fn write_input(&mut self, bytes: &[u8]);

    fn viewport(&self) -> TerminalViewport;
    fn read_visible_text(
        &self,
        max_lines: usize,
        max_columns: usize,
    ) -> String;

    fn scroll(&mut self, command: ScrollCommand);

    fn begin_search(&mut self, query: &str);
    fn clear_search(&mut self);

    fn title(&self) -> Option<&str>;
}
```

Exact signatures should be adapted after exploring `alacritty_terminal`.

The important requirement is ownership:

```text
Workspace
   ↓
TerminalSession
   ↓
OmaTerm Terminal Abstraction
   ↓
Alacritty implementation
```

NOT:

```text
Workspace
   ↓
alacritty_terminal everywhere
```

## 9.3 libghostty as a Future Backend

Ghostty now publicly describes `libghostty` as an embeddable cross-platform terminal library.

This makes a future Ghostty engine realistic.

However:

- API signatures are still evolving;
- GPUI rendering integration must be evaluated;
- Alacritty offers a simpler initial Rust path.

The architecture should make it possible to add:

```text
TerminalEngine
├── AlacrittyEngine
└── GhosttyEngine
```

later.

Do not prematurely implement both.

---

# 10. Terminal Architecture

Do not treat "terminal" as one giant object.

There are separate responsibilities:

```text
PTY
 │
 │ byte stream
 ▼
Terminal Emulator / State Machine
 │
 │ cells, attributes, modes
 ▼
Terminal Renderer
 │
 ▼
GPUI
```

Recommended logical layers:

```text
PtyProcess
TerminalEngine
TerminalRenderer
TerminalSession
```

## 10.1 PtyProcess

Owns:

- PTY master;
- child process;
- input/output;
- resize;
- exit lifecycle;
- process identifiers.

## 10.2 TerminalEngine

Owns:

- VT parsing;
- screen state;
- alternate screen;
- cursor;
- terminal modes;
- scrollback;
- selection-related terminal coordinates;
- terminal events.

## 10.3 TerminalRenderer

Owns:

- converting terminal cell state into GPUI draw operations;
- glyph layout;
- cursor rendering;
- selection rendering;
- visible-range optimization;
- background colors;
- decorations;
- possibly image protocol support much later.

## 10.4 TerminalSession

Application-level durable terminal identity.

Owns/references:

```text
SessionId
working directory
title
PTY
TerminalEngine
renderer state
terminal search state
process metadata
agent metadata later
```

A `TerminalSession` MUST survive UI changes.

---

# 11. PTY Design

Every normal terminal session should start a real shell under a PTY.

The initial shell should normally be resolved from the user's environment/account configuration.

Preferred behavior:

```text
$SHELL
```

with reasonable platform fallback.

The shell should behave naturally:

```text
zsh
bash
fish
```

OmaTerm should not inject unnecessary shell behavior.

Environment variables needed for OmaTerm identity/automation MAY be injected.

Example:

```text
TERM
TERM_PROGRAM
OMATERM
OMATERM_PROJECT_ID
OMATERM_PANE_ID
OMATERM_SESSION_ID
OMATERM_SOCKET
OMATERM_TOKEN
```

Do not lie about terminal capabilities.

Any `TERM_PROGRAM` or terminal capability value must reflect protocols OmaTerm genuinely supports.

---

# 12. Workspace Domain Model

Use stable typed identifiers.

Suggested examples:

```rust
struct WindowId(Uuid);
struct ProjectId(Uuid);
struct TabId(Uuid);
struct PaneId(Uuid);
struct SplitId(Uuid);
struct SessionId(Uuid);
```

Newtypes are preferable to passing raw UUIDs everywhere.

Recommended model:

```rust
pub struct Workspace {
    pub windows: Vec<WorkspaceWindow>,
}

pub struct WorkspaceWindow {
    pub id: WindowId,
    pub projects: Vec<Project>,
    pub selected_project: Option<ProjectId>,
}

pub struct Project {
    pub id: ProjectId,
    pub custom_name: Option<String>,
    pub pinned_directory: Option<PathBuf>,
    pub tabs: Vec<Tab>,
    pub selected_tab: Option<TabId>,
}

pub struct Tab {
    pub id: TabId,
    pub custom_name: Option<String>,
    pub root: PaneNode,
    pub focused_pane: PaneId,
}

pub enum PaneNode {
    Pane(Pane),

    Split {
        id: SplitId,
        axis: SplitAxis,
        fraction: f32,
        first: Box<PaneNode>,
        second: Box<PaneNode>,
    },
}

pub struct Pane {
    pub id: PaneId,
    pub content: PaneContent,
}
```

Initial content:

```rust
pub enum PaneContent {
    Terminal(SessionId),
}
```

Later:

```rust
pub enum PaneContent {
    Terminal(SessionId),
    File(FileId),
    Diff(DiffId),
}
```

Do not add placeholder abstractions for ten hypothetical pane types before they are needed.

The important extensibility boundary is `PaneContent`.

---

# 13. Pane Tree Operations

Implement pane-tree operations in the core independently from GPUI.

The pane model should support:

```text
insert beside target
remove pane
collapse empty parent
change split fraction
find pane
find ancestors
enumerate panes
equalize tree
resolve directional neighbor
```

Core operations should be unit-testable without a desktop window.

Required behavior:

## Split Right

Given:

```text
A
```

produce:

```text
horizontal
├── A
└── B
```

## Split Left

```text
horizontal
├── B
└── A
```

## Split Down

```text
vertical
├── A
└── B
```

## Split Up

```text
vertical
├── B
└── A
```

If A is nested, only A's rectangle should be split.

Example:

```text
Before

vertical
├── A
└── B
```

Split B right:

```text
vertical
├── A
└── horizontal
    ├── B
    └── C
```

Do NOT flatten into:

```text
A | B | C
```

---

# 14. Focus Model

The core should have a concept of:

```text
selected project
selected tab
focused pane
focused terminal session
```

Do not make focus derivation depend entirely on GPUI widget state.

GPUI events should update the workspace model.

The semantic workspace focus then becomes usable by:

- keyboard actions;
- CLI commands;
- future agents;
- session restoration.

---

# 15. Command Architecture

This is one of the most important OmaTerm decisions.

OmaTerm should have an internal semantic command bus.

The UI, CLI, and future agents should converge on this command layer.

Conceptually:

```text
                  ┌───────────────┐
                  │   OmaTerm UI  │
                  └───────┬───────┘
                          │
                          ▼
                 ┌─────────────────┐
                 │ Command Router  │
                 └─────────────────┘
                    ▲           ▲
                    │           │
              omaterm CLI     Agent
```

Below the router:

```text
Workspace
Project
Tab
Pane
Terminal
Git          later
Agents       later
```

## 15.1 Example Command Types

```rust
pub enum OmaCommand {
    Project(ProjectCommand),
    Tab(TabCommand),
    Pane(PaneCommand),
    Terminal(TerminalCommand),
}
```

Example pane commands:

```rust
pub enum PaneCommand {
    Split {
        target: PaneId,
        direction: SplitDirection,
    },

    Close {
        pane: PaneId,
    },

    Focus {
        pane: PaneId,
    },

    Resize {
        split: SplitId,
        fraction: f32,
    },

    Equalize {
        tab: TabId,
    },
}
```

Example terminal commands:

```rust
pub enum TerminalCommand {
    Create {
        project: ProjectId,
        directory: Option<PathBuf>,
    },

    SendBytes {
        session: SessionId,
        data: Vec<u8>,
    },

    RunCommand {
        session: SessionId,
        command: String,
    },

    ReadVisible {
        session: SessionId,
        max_lines: usize,
        max_columns: usize,
    },

    Clear {
        session: SessionId,
    },
}
```

Do not overcommit to exact enum shapes before building the first vertical slice.

Preserve the semantic-command principle.

---

# 16. CLI Design

The CLI binary is:

```text
omaterm
```

Eventually the following style should be possible:

```bash
omaterm project list
```

```bash
omaterm project open ~/Code/myapp
```

```bash
omaterm pane list
```

```bash
omaterm pane split --right
```

```bash
omaterm pane split --down
```

```bash
omaterm pane focus <pane-id>
```

```bash
omaterm terminal list
```

```bash
omaterm terminal new
```

```bash
omaterm terminal run --pane <pane-id> -- cargo test
```

```bash
omaterm terminal send --pane <pane-id> "cargo test"
```

```bash
omaterm terminal read --pane <pane-id> --lines 100
```

```bash
omaterm terminal wait \
  --pane <pane-id> \
  --contains "Server listening"
```

Future agent commands:

```bash
omaterm agent spawn \
  --kind codex \
  --name backend
```

```bash
omaterm agent prompt backend \
  "Implement the API endpoint"
```

```bash
omaterm agent status backend
```

The exact CLI hierarchy may evolve.

Semantic intent must remain stable.

---

# 17. The CLI Must Not Automate the UI

This is prohibited architecture:

```text
agent
 ↓
omaterm CLI
 ↓
simulate keyboard shortcut
 ↓
GPUI
```

Avoid commands such as:

```bash
omaterm press ctrl-shift-d
```

for normal automation.

Instead:

```text
agent
 ↓
semantic CLI command
 ↓
IPC
 ↓
Command Router
 ↓
Workspace Core
```

Example wire intent:

```json
{
  "version": 1,
  "request_id": "01...",
  "method": "pane.split",
  "params": {
    "pane_id": "abc",
    "direction": "right"
  }
}
```

The UI should observe the resulting state change.

It should not be the automation target.

---

# 18. IPC Architecture

For the initial Linux version:

```text
Unix domain socket
```

is preferred.

Recommended path:

```text
$XDG_RUNTIME_DIR/omaterm.sock
```

If XDG runtime directory is unavailable, use a secure fallback with correct ownership/permissions.

Architecture:

```text
                 OmaTerm desktop process
                 ┌─────────────────────┐
                 │ Workspace Core      │
                 │ GPUI                │
                 │ Terminal Sessions   │
                 │ Command Router      │
                 │ IPC Server          │
                 └──────────▲──────────┘
                            │
                       Unix socket
                            │
               ┌────────────┴────────────┐
               │                         │
          omaterm CLI               Coding Agent
```

Do not introduce a separate daemon for v1.

The desktop process can own the server.

A separate `omatermd` may be considered later if headless session ownership becomes a requirement.

---

# 19. IPC Wire Protocol

Use a versioned protocol from the start.

JSON is acceptable initially because:

- easy debugging;
- easy CLI implementation;
- easy agent inspection;
- stable interoperability.

Suggested structure:

```json
{
  "version": 1,
  "request_id": "0192...",
  "method": "terminal.read",
  "params": {
    "pane_id": "abc",
    "lines": 100,
    "columns": 500
  },
  "token": "..."
}
```

Response:

```json
{
  "version": 1,
  "request_id": "0192...",
  "ok": true,
  "result": {
    "text": "..."
  }
}
```

Failure:

```json
{
  "version": 1,
  "request_id": "0192...",
  "ok": false,
  "error": {
    "code": "pane_not_found",
    "message": "No pane with that id exists in this project."
  }
}
```

Use explicit error codes.

Do not require clients to parse human-readable error text.

---

# 20. Capability and Security Model

Every OmaTerm terminal should eventually receive a capability representing its authorized automation scope.

Example environment:

```text
OMATERM=1
OMATERM_PROJECT_ID=<project>
OMATERM_PANE_ID=<pane>
OMATERM_SESSION_ID=<session>
OMATERM_SOCKET=<socket>
OMATERM_TOKEN=<capability>
```

An agent running in Project A should naturally get:

```text
project_scope = A
originating_pane = abc
```

By default:

```text
Agent A → Project A     allowed
Agent A → Project B     denied
```

Cross-project control should require an intentionally broader capability or explicit user action.

Tokens should:

- be random/unpredictable;
- not be guessable IDs;
- be scoped;
- be revocable when sessions close;
- avoid being logged;
- avoid appearing in normal diagnostic output.

---

# 21. Human Approval Boundary

Future agent automation must distinguish routine workspace actions from sensitive decisions.

Safe/normal examples:

```text
create pane
start non-privileged command
read bounded terminal output
focus pane
query pane state
```

Potentially sensitive examples:

```text
send passwords
answer sudo prompts
approve destructive confirmation
accept credentials
approve trust dialog
run privileged commands
cross project boundaries
```

A future API might model:

```rust
enum AutomationAction {
    Safe(SafeAction),
    RequiresUserApproval(SensitiveAction),
}
```

Do not build a system where an agent receives unrestricted keyboard access and therefore implicitly gains the ability to approve anything shown in the terminal.

---

# 22. First-Run Product Scope

The first meaningful OmaTerm milestone should be intentionally narrow.

## OmaTerm 0.1

Required:

- Linux desktop application;
- GPUI window;
- project sidebar;
- multiple projects;
- tabs;
- recursive terminal split panes;
- real PTY shells;
- Alacritty-based terminal engine;
- terminal rendering;
- focus navigation;
- pane resizing;
- pane close;
- session persistence;
- project/tab/pane persistence;
- `omaterm` CLI;
- Unix socket IPC;
- semantic command router.

Do not require Git UI yet.

Do not require file editor yet.

Do not require coding-agent detection yet.

Do not require browser panes.

The most important 0.1 achievement is:

> OmaTerm is already a usable terminal workspace whose panes can be controlled semantically through its CLI.

That architecture will make later features much easier.

---

# 23. Suggested Version Progression

## 0.1 — Terminal Workspace

```text
Projects
Tabs
Recursive splits
Terminal
PTY
Persistence
Command router
CLI
IPC
```

## 0.2 — Developer Context

```text
File tree
Git status
Diff viewer
Command palette
Project search
```

Potential lightweight editor can enter here or 0.3.

## 0.3 — Agent Awareness

```text
agent process recognition
agent aliases
agent status
provider integration hooks
attention indicators
notifications
```

## 0.4 — Agent Automation

```text
agent spawn
agent prompt
agent wait
agent result reads
delegated panes
agent-to-agent coordination
approval boundaries
```

## Later

Evaluate:

```text
Ghostty backend
macOS
Windows
browser panes
plugins/extensions
remote sessions
```

Do not commit to these before the terminal workspace is excellent.

---

# 24. Repository Structure

Recommended initial workspace:

```text
omaterm/
├── Cargo.toml
├── rust-toolchain.toml
├── README.md
├── LICENSE
│
├── crates/
│   ├── omaterm-core/
│   │   └── src/
│   │       ├── lib.rs
│   │       ├── ids.rs
│   │       ├── workspace.rs
│   │       ├── project.rs
│   │       ├── tab.rs
│   │       ├── pane.rs
│   │       ├── command.rs
│   │       └── error.rs
│   │
│   ├── omaterm-terminal/
│   │   └── src/
│   │       ├── lib.rs
│   │       ├── session.rs
│   │       ├── engine.rs
│   │       ├── alacritty.rs
│   │       ├── pty.rs
│   │       ├── events.rs
│   │       └── history.rs
│   │
│   ├── omaterm-protocol/
│   │   └── src/
│   │       ├── lib.rs
│   │       ├── request.rs
│   │       ├── response.rs
│   │       ├── capability.rs
│   │       └── version.rs
│   │
│   ├── omaterm-ipc/
│   │   └── src/
│   │       ├── lib.rs
│   │       ├── server.rs
│   │       └── client.rs
│   │
│   ├── omaterm-state/
│   │   └── src/
│   │       ├── lib.rs
│   │       ├── snapshot.rs
│   │       ├── store.rs
│   │       └── migration.rs
│   │
│   ├── omaterm-ui/
│   │   └── src/
│   │       ├── lib.rs
│   │       ├── app.rs
│   │       ├── window.rs
│   │       ├── sidebar.rs
│   │       ├── tab_bar.rs
│   │       ├── pane_layout.rs
│   │       ├── terminal_view.rs
│   │       └── theme.rs
│   │
│   └── omaterm-cli/
│       └── src/
│           ├── main.rs
│           ├── command.rs
│           └── output.rs
│
└── apps/
    └── omaterm/
        └── src/
            └── main.rs
```

This is a recommendation, not a requirement.

Avoid excessive crate fragmentation if it slows early iteration.

A simpler initial workspace is acceptable:

```text
core
terminal
ui
cli
```

Split further when module boundaries become clear.

---

# 25. Dependency Direction

Preserve a clean dependency graph.

Preferred:

```text
omaterm-ui
    ↓
omaterm-core
    ↓
domain types
```

```text
omaterm-terminal
    ↓
terminal abstraction
```

```text
omaterm-cli
    ↓
omaterm-protocol
    ↓
omaterm-ipc
```

Avoid:

```text
core → gpui
```

for fundamental workspace algorithms.

The recursive pane tree and command definitions should be testable without opening a GPUI application.

Some application coordination may eventually require GPUI-aware state wrappers, but domain algorithms should stay separated where practical.

---

# 26. Async / Concurrency

Terminal IO, subprocesses, file watching, Git commands, and IPC are naturally asynchronous.

Rust async runtime choice may be:

```text
Tokio
```

but inspect GPUI task/executor patterns before adding redundant runtime complexity.

Use one clear ownership model.

Avoid casually mixing:

```text
Tokio runtime
GPUI executor
random OS threads
blocking channels
```

without understanding cancellation and shutdown behavior.

Key principle:

- terminal read loops must never block UI rendering;
- process waits must never block the GPUI main thread;
- rendering should consume efficiently synchronized terminal snapshots/state.

---

# 27. Rendering Strategy

The renderer should draw only what is necessary.

Do not create a heavyweight UI object per terminal cell if GPUI provides a more direct/custom drawing path.

Terminal rendering should ideally be based on:

```text
visible rows
×
visible columns
```

rather than full scrollback.

Optimize later based on measurements, but avoid obviously pathological architecture.

Important future considerations:

- glyph shaping/rasterization;
- monospace assumptions;
- fallback symbols;
- emoji;
- ligatures policy;
- selection;
- cursor styles;
- underline/strikethrough;
- wide characters;
- combining characters;
- Unicode width;
- IME;
- alternate screen;
- mouse mode;
- OSC events.

Terminal correctness matters more than superficial animations.

---

# 28. Hidden Pane Behavior

A pane being hidden because another tab/project is selected should not mean:

```text
stop reading PTY output
```

PTY output must continue to drain.

However, a hidden terminal should not necessarily consume full rendering resources.

Think in three lifetimes:

```text
process lifetime
terminal-state lifetime
renderer visibility lifetime
```

They are related but not identical.

Future optimization can allow:

```text
process alive
terminal parser alive
renderer parked
```

This was an important performance lesson visible in Kero's development history.

---

# 29. Terminal Scrollback

Keep bounded in-memory scrollback initially.

Configuration may eventually allow:

```toml
[terminal]
scrollback-lines = 10000
```

Do not persist unbounded terminal history.

If static history restore is later introduced, store it separately from workspace topology.

Reason:

terminal output may include secrets.

Potential future layout:

```text
$XDG_STATE_HOME/omaterm/
├── workspace-v1.json
└── sessions/
    ├── <id>.scrollback.zst
    └── ...
```

History restore should remain opt-in if persisted output contains sensitive data.

---

# 30. Persistence

Start with a versioned snapshot format.

Example:

```json
{
  "schema_version": 1,
  "windows": [
    {
      "projects": []
    }
  ]
}
```

Do not use unversioned serialized Rust structs as a permanent storage contract.

Persist logical state, not runtime handles.

Persist:

```text
IDs as appropriate
project paths
custom project names
tabs
pane tree
split fractions
focused pane
terminal working directories
selected project
selected tab
sidebar state
```

Do NOT persist:

```text
PTY file descriptors
process handles
raw renderer objects
GPUI entities
thread handles
socket handles
```

On restore:

```text
snapshot
 ↓
rebuild workspace
 ↓
create fresh TerminalSession
 ↓
launch fresh shell in old directory
```

---

# 31. Project Directory Model

Initially a project can have a known root directory.

Eventually implement Kero-like automatic repository awareness:

```text
if pinned directory exists:
    root = pinned directory
else:
    root = nearest enclosing git repository of active terminal cwd
```

Later, optionally detect foreground-agent worktree cwd and temporarily re-root Git/file context.

This behavior should NOT block v0.1.

---

# 32. Git Strategy

When Git functionality is implemented, prefer the user's installed Git CLI.

Do not start by embedding libgit2.

Why:

the system Git automatically respects:

```text
.gitconfig
credential helpers
SSH
GPG
hooks
worktrees
enterprise Git configuration
```

Recommended machine-oriented status command:

```bash
git status --porcelain=v2 -z
```

Set environment/arguments to avoid accidental hidden interactive prompts where appropriate.

The Git UI should remain explicit.

Avoid choosing merge/rebase strategies for users silently.

---

# 33. Diff Strategy

Future diff model:

```text
Diff
└── Files[]
    └── Hunks[]
        └── Lines[]
```

The UI must eventually virtualize large diffs.

Do not render thousands of files and all lines as one enormous element tree.

Syntax highlighting, when added, should run off the UI thread where possible.

---

# 34. Lightweight File Editing

A future editor can support:

```text
open
save
find
replace
syntax highlighting
dirty state
external change detection
```

Without becoming:

```text
VS Code
```

Explicit initial non-features:

```text
LSP
autocomplete
refactoring
debugger
semantic workspace indexing
```

Tree-sitter is a reasonable syntax-highlighting choice when this phase begins.

---

# 35. Command Palette

A command palette should eventually become the user's universal action surface.

Potential shortcut:

```text
Ctrl+Shift+P
```

or an Omarchy-consistent equivalent to be decided during UX design.

Palette may search:

```text
OmaTerm commands
projects
tabs
open sessions
files later
```

Every palette action should invoke semantic commands rather than contain bespoke workspace mutation logic.

---

# 36. Keyboard-First Interaction

OmaTerm should be fully usable without a mouse for core workflows.

Exact shortcuts should be chosen after checking Omarchy conventions and avoiding collisions.

Desired operations include:

```text
new project
new tab
close pane
split right
split down
focus pane left/right/up/down
resize pane
next/previous tab
project switch
command palette
pane zoom later
```

Do not blindly copy macOS Kero shortcuts.

OmaTerm is Linux-first.

---

# 37. Omarchy Integration

Because OmaTerm's identity is derived from Omarchy, the Linux experience should feel intentional.

Potential integrations to evaluate later:

- Omarchy theme detection;
- matching fonts;
- matching border/radius conventions;
- Wayland-native behavior;
- Hyprland-friendly window rules;
- opening current directory from Omarchy launcher;
- package/install script appropriate for Arch-based systems.

Do not tightly couple the core to Omarchy.

The relationship should be:

```text
OmaTerm runs excellently on Omarchy
```

not:

```text
OmaTerm cannot run without Omarchy internals
```

---

# 38. Agent Architecture

Agent features are not required for v0.1, but design around the following future model.

Supported future categories may include:

```text
Codex
Claude Code
Gemini CLI
OpenCode
Aider
Amp
Pi
others
```

Use adapters.

Conceptually:

```rust
trait AgentIntegration {
    fn kind(&self) -> AgentKind;

    fn recognize_process(
        &self,
        process: &ProcessInfo,
    ) -> bool;

    fn lifecycle_support(&self) -> LifecycleSupport;
}
```

Do not encode dozens of agent-specific conditions into terminal rendering code.

---

# 39. Agent State

Suggested model:

```text
created
working
blocked
idle
done
unknown
```

Authority should be explicit.

Example:

```rust
enum AgentStateAuthority {
    Integration,
    Process,
    Command,
}
```

Meaning:

- `Integration` — provider plugin/hook reported lifecycle;
- `Process` — executable identity indicates an agent exists;
- `Command` — OmaTerm itself launched/prompted the process.

Do not silently upgrade uncertain state to definitive "Done".

---

# 40. Future Agent Workflow

Target future experience:

```text
Main Agent
   │
   ├── backend pane
   │      └── Codex
   │
   ├── tests pane
   │      └── Claude
   │
   └── dev server pane
```

The main agent might request:

```text
create background pane
run tests
wait for tests
read result
ask backend agent for status
send follow-up
```

Each operation should route through the same semantic command/IPC system created for the CLI.

This is why CLI architecture is a v0.1 requirement even though full agents are not.

---

# 41. Terminal Reading for Automation

Future agents should not receive infinite raw terminal memory.

Expose bounded reads:

```text
max lines
max columns
current visible viewport
recent output
```

Example:

```bash
omaterm terminal read \
  --pane abc \
  --lines 120 \
  --columns 500
```

Limits should also be enforced server-side.

Do not trust client-provided large values.

Future alternate-screen TUI handling can be more sophisticated, but v0.1 does not need agent transcript extraction.

---

# 42. Wait Primitives

Automation becomes much easier if OmaTerm supports waits directly.

Potential API:

```bash
omaterm terminal wait \
  --pane abc \
  --contains "Server ready" \
  --timeout 30s
```

Future semantic waits:

```text
wait for process exit
wait for terminal idle
wait for agent state
wait for text
wait for port
```

Avoid forcing agents to poll excessively.

---

# 43. Output Format for CLI

Human default:

```text
readable text
```

Machine option:

```bash
omaterm pane list --json
```

JSON responses should use stable fields.

Example:

```json
{
  "panes": [
    {
      "id": "abc",
      "project_id": "p1",
      "tab_id": "t1",
      "kind": "terminal",
      "focused": true,
      "session_id": "s1"
    }
  ]
}
```

Eventually consider making machine-readable output the direct IPC representation.

---

# 44. Error Design

Define error codes centrally.

Examples:

```text
project_not_found
tab_not_found
pane_not_found
terminal_required
session_exited
permission_denied
cross_project_denied
invalid_request
unsupported_version
timeout
shell_busy
```

Errors should contain:

```text
code
message
optional structured details
```

Agents need stable codes.

---

# 45. Logging

Use structured Rust logging, likely:

```text
tracing
```

Categories may include:

```text
workspace
pane
terminal
pty
render
ipc
cli
persistence
agent
git
```

Never log:

```text
capability tokens
passwords
full clipboard data
sensitive terminal payloads by default
```

Add debug instrumentation without making sensitive data the default telemetry.

OmaTerm should not require analytics.

---

# 46. Privacy

Initial product preference:

- no account;
- no cloud requirement;
- no analytics required for core functionality;
- local state;
- local automation socket.

If telemetry is ever introduced, it should be explicitly designed rather than added casually.

---

# 47. Terminal Security

Future terminal features should consider:

## Clipboard

OSC 52 clipboard reads can expose host clipboard content to remote terminal applications.

OmaTerm should eventually implement explicit policy/confirmation rather than silently returning clipboard contents.

## Paste Protection

Multiline or command-like pastes may warrant confirmation.

Do not block basic copy/paste workflows unnecessarily.

## Drag/Drop

Files dropped into terminal should eventually become safely shell-escaped absolute paths.

These features are not all required for the first renderer milestone but must be recognized as real terminal responsibilities.

---

# 48. Wayland

Wayland is first-class.

Test at minimum:

```text
Omarchy / Hyprland
```

Important areas:

- keyboard input;
- modifiers;
- clipboard;
- selection;
- IME;
- scaling;
- multiple monitors;
- cursor;
- drag/drop;
- window focus;
- text rendering.

Do not consider Linux support "done" only because a window opens.

---

# 49. X11

Enable X11 support where GPUI makes this inexpensive.

This improves Linux compatibility.

However, bugs unique to X11 should not prevent the first Wayland/Omarchy-focused milestones unless they indicate a deeper architecture issue.

---

# 50. Cross-Platform Boundary

OS-specific functionality must live behind small interfaces.

Examples:

```rust
trait ProcessInspector { ... }
trait ClipboardProvider { ... }
trait NotificationProvider { ... }
trait PtyProvider { ... }
```

Do not spread:

```rust
#[cfg(target_os = "linux")]
```

through every workspace module.

Future platform work should replace adapters rather than rewrite core workspace logic.

---

# 51. Process Inspection

A future Info panel can expose:

```text
child processes
PID
CPU
memory
listening ports
```

This is highly useful for coding-agent and dev-server workflows.

Interface example:

```rust
trait ProcessInspector {
    fn descendants(&self, pid: ProcessId)
        -> Result<Vec<ProcessInfo>>;

    fn listening_ports(&self, pid: ProcessId)
        -> Result<Vec<ListeningPort>>;
}
```

Linux implementation can use `/proc` and appropriate system/network information.

Do not implement this before terminal sessions are stable unless needed for foreground process tracking.

---

# 52. Performance Requirements

Early implementation should establish basic performance instrumentation.

Measure:

```text
startup time
idle CPU
terminal input latency
terminal throughput
memory per session
memory per visible terminal
memory per hidden terminal
pane resize behavior
scrolling
large output behavior
```

Do not optimize based only on intuition.

However, avoid known bad designs:

- per-cell heavyweight widgets;
- UI-thread blocking process reads;
- synchronous Git commands on UI thread;
- full scrollback rendering;
- recreating terminal engine on tab switch;
- rerendering the entire window for every split drag event if avoidable.

---

# 53. Resource Lifecycle Tests

Terminal apps are vulnerable to subtle resource leaks.

Add stress tests/manual diagnostics for:

```text
create 100 terminals
close 100 terminals
repeat
```

Check:

```text
file descriptors
PTY descriptors
threads
GPU resources
processes
memory
```

Also test:

```text
rapid split
rapid close
resize
project switching
session restoration
```

Kero's changelog contains examples of real terminal resource leaks that only appear after long coding-agent sessions.

Treat lifecycle correctness as a major feature.

---

# 54. Testing Strategy

## Unit Tests

Must heavily cover:

```text
pane tree insertion
pane removal
tree collapse
split fraction changes
focus resolution
serialization
migration
command validation
protocol parsing
capability scope
```

## Integration Tests

Cover:

```text
PTY starts shell
terminal receives output
terminal accepts input
terminal resize
terminal exits
IPC request/response
CLI → desktop command
workspace persistence
```

## UI Tests

Use GPUI test support where appropriate.

Focus on high-value interactions rather than screenshot-testing every component.

## Manual Test Matrix

At minimum:

```text
Omarchy + Wayland
another Wayland compositor if available
X11 session
zsh
bash
fish if practical
tmux
vim/neovim
htop/btop
Claude/Codex/OpenCode later
```

---

# 55. MVP Acceptance Criteria

OmaTerm 0.1 is successful when all of the following are true.

## Application

- starts reliably on Omarchy;
- creates a default project;
- can create multiple projects;
- project switching is fast.

## Terminal

- launches a real shell;
- normal shell config works;
- interactive TUIs work;
- terminal resizes correctly;
- Unicode works reasonably;
- copy/paste works;
- scrollback works;
- closing a pane terminates its terminal correctly.

## Tabs

- multiple tabs per project;
- tab switch preserves terminal state;
- tabs can be closed;
- selected tab persists.

## Panes

- split right;
- split down;
- nested splits;
- focus between panes;
- resize;
- close/collapse;
- terminal sessions survive layout state updates.

## Persistence

After restart:

- projects return;
- tabs return;
- pane layout returns;
- terminals start fresh shells in remembered working directories;
- selected project/tab is restored.

## CLI/IPC

While OmaTerm is running:

```bash
omaterm project list
```

works.

```bash
omaterm pane list
```

works.

```bash
omaterm pane split --right
```

causes the semantic split in the active project.

```bash
omaterm terminal send ...
```

can write to an authorized terminal.

```bash
omaterm terminal read ...
```

can return bounded terminal text.

The CLI must achieve this through IPC and the same command layer used by the UI.

---

# 56. Explicit Non-Goals for 0.1

Do NOT block release on:

```text
browser panes
Git panel
diff viewer
file editor
agent detection
agent coordination
LSP
debugger
extensions
cloud sync
SSH manager UI
Docker manager
database tools
session daemon
Windows
macOS
perfect theme compatibility
Ghostty backend
```

A focused terminal workspace is sufficient.

---

# 57. Design Language

OmaTerm should visually feel:

```text
calm
minimal
fast
technical
Omarchy-compatible
terminal-centric
```

Avoid:

```text
large dashboard cards
excessive borders
decorative gradients
huge toolbars
IDE-like visual density
persistent noisy status widgets
```

Terminal content should dominate visual space.

Project/sidebar chrome should remain compact.

---

# 58. Future Theme Support

Eventually support a shared theme that can influence:

```text
terminal background
terminal foreground
ANSI colors
app background
sidebar
tabs
selection
borders
```

Because Omarchy is important to the product identity, consider importing/mapping Omarchy themes later.

Do not build a complex theme engine before the terminal is stable.

---

# 59. Packaging

First-class Linux packaging should eventually include:

```text
Arch package / PKGBUILD
```

because Omarchy users are Arch-based.

Also consider:

```text
standalone tarball
AppImage
```

later.

`.deb`/`.rpm` can follow if broader Linux distribution matters.

Do not let packaging complexity delay the initial development binary.

---

# 60. CLI Launch Behavior

Eventually:

```bash
omaterm
```

should launch or focus OmaTerm.

Potential:

```bash
omaterm .
```

opens current directory as a project.

```bash
omaterm ~/Code/foo
```

opens a project rooted there.

Potential direct command:

```bash
omaterm ~/Code/foo -- cargo test
```

could create project + terminal and run argv.

Do not mix launcher parsing with running-app IPC in an unstructured way.

The CLI can determine:

```text
Is app running?
├── yes → IPC
└── no  → launch desktop app with requested initial action
```

Exact implementation can follow after core IPC exists.

---

# 61. Configuration

Use TOML.

Recommended:

```text
~/.config/omaterm/config.toml
```

Initial configuration may include:

```toml
[terminal]
font-family = "JetBrains Mono"
font-size = 13
scrollback-lines = 10000

[appearance]
theme = "system"

[automation]
enabled = true
```

Do not overdesign configuration before options exist.

Unknown or invalid values should fail safely with clear logs/errors.

---

# 62. Architecture Rule: No Duplicate Business Logic

If the UI performs:

```text
split focused pane right
```

and CLI performs:

```text
split focused pane right
```

they MUST share the same core implementation.

Do not implement:

```text
UI-specific pane splitting logic
+
CLI-specific pane splitting logic
```

The UI and CLI should construct the same command.

---

# 63. Architecture Rule: IDs Over Screen Coordinates

Automation should reference:

```text
project_id
tab_id
pane_id
session_id
```

not:

```text
left pane
third tab pixel position
screen x/y
```

Human-facing CLI may allow convenience selectors.

The protocol should resolve to stable IDs.

---

# 64. Architecture Rule: Bounded External Inputs

All automation fields should have limits.

Examples:

```text
maximum prompt bytes
maximum argv count
maximum argument size
maximum terminal read lines
maximum terminal read columns
maximum wait timeout
```

Reject terminal control characters where a structured textual field should not contain them.

Do not allow a convenient local socket to become an unbounded memory interface.

---

# 65. Architecture Rule: Protocol Versioning

Every IPC request should identify protocol version.

Server must be able to reject:

```text
unsupported protocol version
```

cleanly.

Do not tie protocol version directly to desktop application semver.

Example:

```text
OmaTerm app 0.4.2
automation protocol v1
```

is valid.

---

# 66. Architecture Rule: Cancellation

Long waits should support:

- timeout;
- caller disconnect;
- application shutdown;
- pane/session close.

Do not leak asynchronous waiter tasks.

---

# 67. Architecture Rule: Shutdown

When OmaTerm exits:

1. stop accepting new IPC;
2. persist current workspace;
3. close/terminate terminal sessions;
4. reap processes;
5. close PTYs;
6. remove/close socket;
7. release GPU resources.

Do not rely on process termination alone as normal resource cleanup.

---

# 68. Implementation Sequence

The implementation agent should work in vertical slices.

## Slice 1 — GPUI Boot

Deliver:

```text
GPUI app
one window
basic background
basic text
Wayland test
X11 build support
```

## Slice 2 — Pure Pane Tree

Deliver:

```text
PaneNode
Split
insert
remove
resize
tests
```

Render placeholder panes.

No terminal yet.

## Slice 3 — One Terminal

Deliver:

```text
PTY
alacritty_terminal
GPUI terminal renderer
keyboard input
resize
scrollback
```

## Slice 4 — Multiple Pane Terminals

Wire pane leaves to long-lived `TerminalSession`s.

Verify terminals survive focus/layout changes.

## Slice 5 — Projects/Tabs

Add:

```text
Project
Tab
project sidebar
tab bar
focus model
```

## Slice 6 — Persistence

Versioned snapshot.

Restart restores projects/tabs/layout and launches fresh shells.

## Slice 7 — Command Router

Move UI mutations behind semantic command layer.

## Slice 8 — IPC

Unix socket server.

## Slice 9 — CLI

Implement:

```text
project list
pane list
pane split
terminal send
terminal read
```

At this point the fundamental OmaTerm architecture is proven.

---

# 69. Agent Instructions for Starting the Repository

Before writing substantial implementation:

1. Read the current GPUI examples and platform initialization code.
2. Read Zed's current terminal crate architecture for learning only.
3. Read Zed's terminal-view architecture for rendering/focus patterns for learning only.
4. Do NOT copy GPL Zed implementation wholesale unless OmaTerm intentionally adopts compatible licensing.
5. Inspect `alacritty_terminal` APIs from the version selected.
6. Create a small terminal rendering spike before building the complete workspace.
7. Validate on actual Omarchy/Wayland early.
8. Keep pane-tree code independent of GPUI.
9. Create semantic command types before implementing CLI.
10. Do not implement agents until CLI/IPC control is stable.

---

# 70. Licensing

This must be considered before copying any implementation.

## Kero

Kero is GPLv3.

Use it as:

```text
behavioral reference
product reference
architecture reference
```

unless the project intentionally wants GPL-derived implementation obligations.

## Zed

Zed's terminal-related crates are GPL-3.0-or-later.

Do not copy substantial GPL implementation into a differently licensed OmaTerm without deliberately choosing that licensing direction.

## GPUI

GPUI itself is separately usable under its applicable repository licensing; confirm the exact dependency/license state at implementation time.

## Alacritty / Dependencies

Audit exact dependency licenses before release.

Maintain a third-party license inventory from early development.

---

# 71. Reference Architecture Diagram

Target architecture:

```text
                               OmaTerm

┌──────────────────────────────────────────────────────────────┐
│                           GPUI                               │
│                                                              │
│ Project Sidebar     Tabs        Command Palette later       │
│                                                              │
│                  Recursive Pane Layout                       │
│                                                              │
│       ┌────────────────┬────────────────┐                    │
│       │ Terminal A     │ Terminal B     │                    │
│       │                │                │                    │
│       │ Claude         │ cargo test     │                    │
│       └────────────────┴────────────────┘                    │
└──────────────────────────────┬───────────────────────────────┘
                               │
                        semantic commands
                               │
┌──────────────────────────────▼───────────────────────────────┐
│                         Rust Core                            │
│                                                              │
│ Workspace                                                    │
│ └─ Project                                                   │
│    └─ Tab                                                    │
│       └─ PaneTree                                            │
│          └─ TerminalSession                                  │
│                                                              │
│ Command Router                                               │
│ Terminal Registry                                            │
│ Persistence                                                  │
│ Capability Registry                                          │
└──────────────────────┬───────────────────────┬───────────────┘
                       │                       │
                Terminal subsystem          IPC Server
                       │                       │
              TerminalEngine                  │
                       │                       │
            AlacrittyEngine                    │
                       │                 Unix socket
                      PTY                      │
                       │           ┌───────────┴───────────┐
                  bash/zsh/fish    │                       │
                                   │                       │
                              omaterm CLI             AI Agent
```

---

# 72. Architectural Priorities

In order:

1. **Correct long-lived terminal sessions**
2. **Clean recursive pane model**
3. **Excellent Linux/Wayland input/rendering**
4. **Semantic command architecture**
5. **Reliable IPC/CLI**
6. **Persistence**
7. **Performance/resource lifecycle**
8. **Developer context features**
9. **Agent integrations**
10. **Additional platforms**

Do not reverse this order by building impressive agent UI before the terminal foundation is trustworthy.

---

# 73. Technical Decisions Summary

| Area | Decision |
|---|---|
| Product | Terminal-first developer workspace |
| Name | OmaTerm |
| Primary environment | Omarchy |
| Primary OS | Linux |
| Display | Wayland first, X11 supported |
| Language | Rust |
| UI | GPUI |
| Browser pane | Not required |
| Terminal engine v1 | `alacritty_terminal` |
| Terminal engine architecture | OmaTerm abstraction |
| PTY | Native/Rust PTY layer |
| Layout | Recursive binary pane tree |
| Terminal ownership | Long-lived session independent of view |
| Persistence | Versioned workspace snapshots |
| Running process restoration | No; fresh shell on relaunch |
| Config | TOML |
| IPC | Unix domain socket |
| CLI | `omaterm` |
| Automation | Semantic commands |
| Agent scope | Project-scoped capabilities |
| Git later | system `git` CLI |
| Syntax later | Tree-sitter |
| Daemon | Not initially |
| Full IDE features | Non-goal |

---

# 74. Questions the Implementation Agent May Resolve Independently

The following are implementation details, not reasons to stop and ask for product clarification unless they materially affect architecture:

- exact crate names;
- whether UUID/ULID is used internally;
- exact GPUI component structure;
- channel implementation;
- exact snapshot filename;
- exact terminal scrollback default;
- exact error Rust types;
- exact CLI argument parser;
- exact logging subscriber configuration;
- test helper layout;
- minor visual spacing;
- internal builder patterns.

Use reasonable Rust conventions.

Document important deviations.

---

# 75. Questions That Require Strong Evidence Before Changing

Do not casually change:

```text
Rust
GPUI
Linux first
terminal-first scope
recursive pane tree
long-lived TerminalSession
semantic command bus
Unix-socket CLI architecture
project-scoped agent capabilities
no browser panes for current scope
no full IDE scope
```

If one must change, provide:

1. concrete blocker;
2. reproduction/evidence;
3. alternatives considered;
4. migration cost;
5. recommended replacement.

---

# 76. Initial Research References

Use these as primary references.

## Kero

Repository:

```text
https://github.com/egoist/kero
```

Website/docs:

```text
https://kero.sh/
```

High-value Kero files:

```text
PRODUCT.md
kero/Panes.swift
kero/Project.swift
kero/TerminalBackend.swift
kero/TerminalSession.swift
kero/TerminalManager.swift
kero/SessionStore.swift
kero/AgentAutomation.swift
kero/KeroAutomationProtocol.swift
kero/KeroAutomationRouter.swift
kero/KeroAutomationCommandLine.swift
kero/GitStatusModel.swift
kero/DiffViewerView.swift
```

Useful docs:

```text
web/content/docs/projects.mdx
web/content/docs/panes.mdx
web/content/docs/terminal.mdx
web/content/docs/automation.mdx
web/content/docs/git.mdx
web/content/docs/files.mdx
```

## GPUI

```text
https://github.com/zed-industries/zed/tree/main/crates/gpui
```

GPUI README currently documents:

```text
Linux / FreeBSD: Wayland and/or X11
macOS: Metal
Windows: Win32/DirectWrite
```

It also explicitly warns GPUI is pre-1.0 and breaking changes occur.

## Zed Terminal

Reference only:

```text
https://github.com/zed-industries/zed/tree/main/crates/terminal
https://github.com/zed-industries/zed/tree/main/crates/terminal_view
```

The current terminal crate depends on:

```text
alacritty_terminal
gpui
```

Do not copy GPL implementation blindly.

## Ghostty

```text
https://github.com/ghostty-org/ghostty
```

Ghostty currently describes `libghostty` as an embeddable cross-platform terminal library.

Minimal libghostty example:

```text
https://github.com/ghostty-org/ghostling
```

`libghostty` remains a future OmaTerm terminal-backend candidate.

---

# 77. Final Direction

OmaTerm should not be implemented as:

```text
another terminal emulator with tabs
```

and it should not become:

```text
another VS Code clone
```

The intended product is:

> **A fast Linux-first terminal workspace, designed from the beginning so every important workspace action is semantically controllable through a CLI and can later be safely orchestrated by coding agents.**

The critical architecture is therefore not just rendering a shell.

It is the combination of:

```text
projects
+
tabs
+
recursive pane trees
+
long-lived terminal sessions
+
semantic commands
+
local IPC
+
CLI
+
project-scoped capabilities
```

That is the foundation.

If that foundation is correct, Git, diffs, files, agent status, and multi-agent orchestration can be added incrementally without rewriting the application.

If that foundation is wrong, agent automation will become fragile and the product will collapse into UI scripting.

Build the foundation first.

---

# 78. First Implementation Objective

The first agent implementation should aim for this demonstrable flow:

```bash
# Start OmaTerm
omaterm
```

OmaTerm opens with a project containing a working shell.

The user creates splits and tabs.

Then from one of those terminals:

```bash
omaterm pane list
```

returns the current pane IDs.

Then:

```bash
omaterm pane split --right
```

creates another terminal pane through the semantic command router.

Then:

```bash
omaterm terminal run --pane <new-pane> -- cargo test
```

starts a command there.

Then:

```bash
omaterm terminal read --pane <new-pane> --lines 50
```

returns bounded output.

If this works reliably, OmaTerm has already proven the architecture required for future agent control.

That should be considered the first major technical milestone.

---

# 79. Implementation Agent Directive

Proceed with implementation using this document as the architectural baseline.

Prioritize a thin vertical slice over broad unfinished scaffolding.

The desired first progression is:

```text
GPUI window
→ pane tree
→ one real terminal
→ multiple terminals
→ projects/tabs
→ persistence
→ semantic commands
→ IPC
→ CLI
```

At every step:

- keep domain logic testable;
- avoid UI-specific business logic;
- validate on Omarchy/Wayland;
- measure resource lifecycle;
- preserve future agent controllability;
- avoid adding features outside the current milestone.

The product can become sophisticated later.

The foundation must stay simple, explicit, and reliable.
