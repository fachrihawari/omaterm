# Git worker shutdown ownership

Changed only `apps/omaterm/src/git_sync_panel.rs` in this follow-up. Main-window teardown integration is owned by the parent agent.

API: `PendingSync::take_shutdown_thread(self) -> Option<std::thread::JoinHandle<()>>` consumes pending state and transfers its worker to asynchronous teardown. Normal completion first checks `is_finished`, then joins the worker. Panic and channel disconnect return an explicit failure.

| Scenario | Invocation | Binary observable | Artifact |
| --- | --- | --- | --- |
| Production start retains worker for shutdown, pre-fix | `cargo test -p omaterm --bin omaterm-desktop git_sync_panel::tests::started_worker_remains_owned_for_shutdown -- --exact` | FAIL: `started Git worker must remain owned until joined`, 1 failed | `shutdown-red.log` |
| Production start retains worker, post-fix | `cargo test -p omaterm --bin omaterm-desktop git_sync_panel:: -- --nocapture` | `started_worker_remains_owned_for_shutdown ... ok` | `shutdown-green.log` |
| Teardown joins a deliberately gated worker | Same scoped invocation | `shutdown_joins_worker_before_reporting_completion ... ok`; join cannot report completion before release, worker exit observed after join | `shutdown-green.log` |
| UI completion does not detach or block on a still-running worker | Same scoped invocation | `completion_waits_for_worker_exit_without_blocking_ui ... ok`; sent result remains pending until worker exits | `shutdown-green.log` |
| Owned worker panic clears pending with explicit failure | Same scoped invocation | `panicked_owned_worker_reports_failure_and_clears_pending ... ok`; intentional fixture panic printed and test passes | `shutdown-green.log` |
| Existing disconnect, project identity, failures, real local Git actions preserved | Same scoped invocation | 9 passed, 0 failed | `shutdown-green.log` |

Formatting and patch whitespace: `rustfmt --edition 2024 --check apps/omaterm/src/git_sync_panel.rs` and `git diff --check -- apps/omaterm/src/git_sync_panel.rs` both exit 0 (`shutdown-checks.log`). Workspace gates and desktop shutdown integration remain the parent agent's verification responsibility.
