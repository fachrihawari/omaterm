# UI/UX logic review

codeQualityStatus: WATCH
recommendation: REQUEST_CHANGES

Scope: current uncommitted UI/UX and light-theme changes, emphasizing compact shell/input ownership, Git sync lifecycle, and terminal sizing/rendering. No production files edited. No loop plan exists (`omo-agent-toolkit ulw-loop status --json`: ULW_LOOP_PLAN_MISSING), so this is the fallback report path. Goal and criteria came from the parent assignment and `docs/evidence/ui-ux-audit.md`; no separate notepad path was supplied. This scoped review is not exhaustive native acceptance.

## CRITICAL
None identified.

## HIGH
None identified.

## MEDIUM

1. **Compact mode makes explicitly requested panels unreachable.** `apps/omaterm/src/main.rs:19154` through the following visibility assignments unconditionally suppress Projects and Inspector under 720 logical pixels. The panel commands around `main.rs:10900` only update the visibility preference; they cannot override compact suppression. Reproduce at the supported 640px width: press Ctrl+Shift+G/E/I or click an inspector toggle, and no requested panel appears. Project sidebar behaves likewise with Ctrl+B. Users must widen the window to access these functions. Preserve terminal width through an overlay or another explicit compact panel access path. Source control flow establishes the issue; the audit's compact capture claim proves automatic collapse only, and does not demonstrate a panel access path. I did not independently drive this native scenario.

2. **Toolbar test mirrors its implementation threshold.** `apps/omaterm/src/main.rs:21573`: `terminal_toolbar_stays_off_narrow_grids` derives both test inputs from `MIN_TOOLBAR_COLUMNS`, then checks the comparison implemented by `terminal_toolbar_fits`. Changing that constant to a harmful value leaves this test green; removing the call from rendering also leaves it green. It does not protect the claimed unobscured-terminal behavior. Remove this redundant test or exercise actual toolbar visibility with independently chosen narrow/wide layouts. This violates the programming and remove-ai-slops test perspectives, at MEDIUM severity as requested.

## LOW

- `apps/omaterm/src/main.rs:20262` and `:20278`: both color helpers retain an `inverse` parameter and inverse branches although every current caller passes false after inversion moved into `terminal_foreground`/paint selection. This is residual complexity, not an established runtime defect.

## Skill-perspective check

Loaded `programming/SKILL.md`, its Rust README, and `remove-ai-slops/SKILL.md` (including the categories and test guidance). Reviewed scoped production code and tests for implementation mirroring, deletion-only assertions, needless parsing/normalization and abstractions. The toolbar test violates both perspectives. GitInput's sanitization occurs at the external user-input boundary and is justified; its grapheme and byte-limit tests exercise observable editing behavior. The extracted sync lifecycle and Git field modules provide real ownership/reuse boundaries. No brittle prose/prompt tests or deletion-only tests identified in the scoped changes.

## Verification and evidence limits

- Independently ran `cargo test -p omaterm git_sync_panel -- --nocapture`: 9 passed, 0 failed; includes real local Git remote publish/failure and worker shutdown cases. The printed panic belongs to the deliberate worker-panic test. Initial invocation mistakenly used `omaterm-app`, failed package selection, and was corrected.
- Inspected `docs/evidence/ui-ux-audit.md`, `.omo/evidence/git-sync-20261008/shutdown-evidence.md`, `.omo/evidence/git-text-fields/README.md`, and tails of `/tmp/omaterm-ux-audit/workspace-tests.log` and `clippy.log`. The logs are not fresh independent whole-workspace verification. No native approval claimed.
- The audit explicitly records incomplete native surface coverage, so it does not misleadingly claim complete acceptance.

## Blockers before approval

- Provide an explicit path to reveal Projects/Inspector at the supported compact window size.
- Remove or replace the threshold-mirroring toolbar test.

