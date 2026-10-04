# Blueprint Coverage Map

This document maps every section of the authoritative
[OMATERM_AGENT_BLUEPRINT.md](../OMATERM_AGENT_BLUEPRINT.md) to the milestone or
document that owns its implementation or verification. It exists to make
coverage gaps explicit: sections that are intentionally narrative, deferred to a
later version, or not yet owned appear as such rather than being hidden.

The blueprint governs architecture; milestone documents define execution. A
section is **owned** when a milestone spec, acceptance row, or standing
verification contract implements and tests it.

## How to read this map

- **Owning milestone/doc** — the milestone or document that carries the contract.
- **Status** — `delivered` means the owned baseline is implemented with the
  documented limits; it is not a claim that every later milestone has release
  evidence. `in progress` has an active owner but unmet acceptance gates.
  `deferred` is future-version work, `narrative` is a vision/principle/decision
  summary with no separate milestone, and `unowned` is a real gap with no owner.

## Coverage Table

| § | Section | Owning milestone/doc | Status |
|---|---|---|---|
| 1 | Purpose of This Document | [00-overview](00-overview.md) | narrative |
| 2 | Project Name | [00-overview](00-overview.md) | narrative |
| 3 | Product Vision | [00-overview](00-overview.md) | narrative |
| 4 | Primary User | [00-overview](00-overview.md) | narrative |
| 5 | Product Principles | [00-overview](00-overview.md), [acceptance-matrix](acceptance-matrix.md) | narrative |
| 6 | What We Learned From Kero | [00-overview](00-overview.md), [M2](02-milestone-2-pane-tree.md), [M18](18-milestone-18-process-panel.md) | delivered |
| 7 | Current Scope Decisions | [00-overview](00-overview.md), [M11](11-milestone-11-v01-closure.md) | narrative |
| 8 | UI Framework Decision | [M1](01-milestone-1-gpui-boot.md) | delivered |
| 9 | Terminal Engine Decision | [M3](03-milestone-3-single-terminal.md) | delivered |
| 10 | Terminal Architecture | [M3](03-milestone-3-single-terminal.md), [M4](04-milestone-4-multi-terminal.md) | delivered |
| 11 | PTY Design | [M3](03-milestone-3-single-terminal.md) | delivered |
| 12 | Workspace Domain Model | [M2](02-milestone-2-pane-tree.md), [M5](05-milestone-5-projects-tabs.md) | delivered |
| 13 | Pane Tree Operations | [M2](02-milestone-2-pane-tree.md) | delivered |
| 14 | Focus Model | [M2](02-milestone-2-pane-tree.md), [M5](05-milestone-5-projects-tabs.md) | delivered |
| 15 | Command Architecture | [M7](07-milestone-7-command-router.md) | delivered |
| 16 | CLI Design | [M9](09-milestone-9-cli.md) | delivered |
| 17 | The CLI Must Not Automate the UI | [M7](07-milestone-7-command-router.md), [M9](09-milestone-9-cli.md) | delivered |
| 18 | IPC Architecture | [M8](08-milestone-8-ipc.md) | delivered |
| 19 | IPC Wire Protocol | [M8](08-milestone-8-ipc.md) | delivered |
| 20 | Capability and Security Model | [M8](08-milestone-8-ipc.md) | delivered |
| 21 | Human Approval Boundary | [M8](08-milestone-8-ipc.md) (current destructive-action confirmation), future agent approval boundary in [M11](11-milestone-11-v01-closure.md) | deferred (v0.4) |
| 22 | First-Run Product Scope | [M11](11-milestone-11-v01-closure.md), [acceptance-matrix](acceptance-matrix.md) | delivered |
| 23 | Suggested Version Progression | [00-overview](00-overview.md) | narrative |
| 24 | Repository Structure | [00-overview](00-overview.md) | delivered |
| 25 | Dependency Direction | [00-overview](00-overview.md) | delivered |
| 26 | Async / Concurrency | [M4](04-milestone-4-multi-terminal.md), [M7](07-milestone-7-command-router.md) | delivered |
| 27 | Rendering Strategy | [M3](03-milestone-3-single-terminal.md) | delivered |
| 28 | Hidden Pane Behavior | [M4](04-milestone-4-multi-terminal.md), [M5](05-milestone-5-projects-tabs.md) | delivered |
| 29 | Terminal Scrollback | [M3](03-milestone-3-single-terminal.md), [M10](10-milestone-10-history-recovery.md) | delivered |
| 30 | Persistence | [M6](06-milestone-6-persistence.md) | delivered |
| 31 | Project Directory Model | [M6](06-milestone-6-persistence.md), [M12](12-milestone-12-project-context.md) | delivered |
| 32 | Git Strategy | [M14](14-milestone-14-git-status.md) | delivered |
| 33 | Diff Strategy | [M15](15-milestone-15-diff-viewer.md) | in progress |
| 34 | Lightweight File Editing | [M19](19-milestone-19-editor.md) | in progress |
| 35 | Command Palette | [M16](16-milestone-16-palette.md) | in progress |
| 36 | Keyboard-First Interaction | [M5](05-milestone-5-projects-tabs.md), [M16](16-milestone-16-palette.md) | in progress |
| 37 | Omarchy Integration | — | unowned |
| 38 | Agent Architecture | — | deferred (v0.3) |
| 39 | Agent State | — | deferred (v0.3) |
| 40 | Future Agent Workflow | — | deferred (v0.4) |
| 41 | Terminal Reading for Automation | [M9](09-milestone-9-cli.md) (bounded read delivered) | delivered |
| 42 | Wait Primitives | [M11](11-milestone-11-v01-closure.md) (API design note only) | deferred (v0.4) |
| 43 | Output Format for CLI | [M9](09-milestone-9-cli.md) | delivered |
| 44 | Error Design | [M7](07-milestone-7-command-router.md), [M8](08-milestone-8-ipc.md) | delivered |
| 45 | Logging | [M11](11-milestone-11-v01-closure.md) | delivered |
| 46 | Privacy | [M11](11-milestone-11-v01-closure.md), [acceptance-matrix](acceptance-matrix.md) | in progress |
| 47 | Terminal Security | [M3](03-milestone-3-single-terminal.md), [M11](11-milestone-11-v01-closure.md) | delivered |
| 48 | Wayland | [M1](01-milestone-1-gpui-boot.md), [acceptance-matrix](acceptance-matrix.md) | delivered |
| 49 | X11 | [M1](01-milestone-1-gpui-boot.md) (build evidence; runtime unavailable) | delivered with limits |
| 50 | Cross-Platform Boundary | [M1](01-milestone-1-gpui-boot.md), [M11](11-milestone-11-v01-closure.md) | delivered |
| 51 | Process Inspection | [M18](18-milestone-18-process-panel.md) | in progress |
| 52 | Performance Requirements | [M11](11-milestone-11-v01-closure.md), [M17](17-milestone-17-v02-closure.md), [acceptance-matrix](acceptance-matrix.md) | in progress |
| 53 | Resource Lifecycle Tests | [M4](04-milestone-4-multi-terminal.md), [M11](11-milestone-11-v01-closure.md), [M17](17-milestone-17-v02-closure.md) | in progress |
| 54 | Testing Strategy | [acceptance-matrix](acceptance-matrix.md), all milestone specs | delivered |
| 55 | MVP Acceptance Criteria | [acceptance-matrix](acceptance-matrix.md) | delivered |
| 56 | Explicit Non-Goals for 0.1 | [M11](11-milestone-11-v01-closure.md), [00-overview](00-overview.md) | delivered |
| 57 | Design Language | [ui-v5-pixel-perfect-plan](ui-v5-pixel-perfect-plan.md), [fidelity correction plan](ui-v5-fidelity-correction-plan.md) | in progress |
| 58 | Future Theme Support | — | deferred (post-v0.2) |
| 59 | Packaging | [M11](11-milestone-11-v01-closure.md) | delivered with limits |
| 60 | CLI Launch Behavior | [M9](09-milestone-9-cli.md), [M11](11-milestone-11-v01-closure.md) | delivered |
| 61 | Configuration | [M11](11-milestone-11-v01-closure.md) | delivered |
| 62 | Rule: No Duplicate Business Logic | [M7](07-milestone-7-command-router.md) | delivered |
| 63 | Rule: IDs Over Screen Coordinates | [M2](02-milestone-2-pane-tree.md), [M7](07-milestone-7-command-router.md) | delivered |
| 64 | Rule: Bounded External Inputs | [M8](08-milestone-8-ipc.md), [M18](18-milestone-18-process-panel.md) | delivered |
| 65 | Rule: Protocol Versioning | [M8](08-milestone-8-ipc.md) | delivered |
| 66 | Rule: Cancellation | [M7](07-milestone-7-command-router.md), [M19](19-milestone-19-editor.md) | in progress |
| 67 | Rule: Shutdown | [M6](06-milestone-6-persistence.md), [M8](08-milestone-8-ipc.md), [M19](19-milestone-19-editor.md) | in progress |
| 68 | Implementation Sequence | [00-overview](00-overview.md) | narrative |
| 69 | Agent Instructions for Starting the Repository | [AGENTS.md](../AGENTS.md) | narrative |
| 70 | Licensing | [dependencies](dependencies.md), [LICENSE-MIT](../LICENSE-MIT) | delivered |
| 71 | Reference Architecture Diagram | [00-overview](00-overview.md), [AGENTS.md](../AGENTS.md) | narrative |
| 72 | Architectural Priorities | [AGENTS.md](../AGENTS.md) | narrative |
| 73 | Technical Decisions Summary | [00-overview](00-overview.md) | narrative |
| 74 | Questions the Agent May Resolve | [AGENTS.md](../AGENTS.md) | narrative |
| 75 | Questions Requiring Evidence | [AGENTS.md](../AGENTS.md) | narrative |
| 76 | Initial Research References | — | narrative |
| 77 | Final Direction | [00-overview](00-overview.md) | narrative |
| 78 | First Implementation Objective | [M9](09-milestone-9-cli.md), [acceptance-matrix](acceptance-matrix.md) | delivered |
| 79 | Implementation Agent Directive | [AGENTS.md](../AGENTS.md) | narrative |

## Open gaps

These sections are real gaps rather than deliberate narrative or version
deferrals. Each needs a decision before it can be called covered.

| § | Section | Gap | Suggested resolution |
|---|---|---|---|
| 37 | Omarchy Integration | No milestone owns theme detection, Hyprland window rules, or launcher integration; only sketched in the UI plans. | Add a small "Omarchy integration" slice (theme/font detection, window-rule doc, launcher entry) or record an explicit deferral. |
| 46 | Privacy | Acceptance row now exists but is partial: no dedicated no-egress network test or user-facing statement recorded yet. | Complete the acceptance row (no core network egress, local-only state) or record an explicit limit. |
| 58 | Future Theme Support | Tracked as "future work" in status only; no owner in the milestone index. | Keep deferred but link it from the roadmap with an explicit "no theme engine before terminal stability" note. |
| 23 | Project/content search follow-up | §23 names project search, while current M13/M16 only provide filename and workspace-ID search; content search and the intended project-search scope have no owner. | Define the intended search types and assign a post-v0.2 milestone, or explicitly defer both. |
| 61 | Automation disable enforcement | `automation.enabled=false` is parsed but intentionally leaves IPC enabled; the gap is disclosed but not assigned. | Assign enforcement to a security/configuration milestone before advertising it as an operational switch. |

Sections 38–42 belong to the agent roadmaps; §51 belongs to M18. They have
owners even though their acceptance remains incomplete.

## Maintenance

When a milestone changes status, update its owner column here and keep the
[acceptance-matrix](acceptance-matrix.md) and [status](status.md) in sync.
`python3 scripts/check-docs.py` verifies that every blueprint section appears
exactly once in this table, alongside local links/references and bidirectional
CLI/IPC method-table coverage. Ownership and evidence judgments remain manual.
