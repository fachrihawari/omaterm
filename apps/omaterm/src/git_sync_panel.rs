//! Project-scoped ownership and completion of background Git remote actions.

use std::path::PathBuf;
use std::sync::mpsc::{self, Receiver, TryRecvError};
use std::time::Instant;

use omaterm_context::GitSyncReport;
use omaterm_core::{GitCommand, ProjectId};

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Operation {
    Fetch { remote: Option<String> },
    Pull { remote: Option<String> },
    Push { set_upstream: bool },
}

impl Operation {
    pub fn from_command(command: GitCommand) -> Option<(ProjectId, Self)> {
        match command {
            GitCommand::SyncFetch { project, remote } => Some((project, Self::Fetch { remote })),
            GitCommand::SyncPull { project, remote } => Some((project, Self::Pull { remote })),
            GitCommand::SyncPush {
                project,
                set_upstream,
            } => Some((project, Self::Push { set_upstream })),
            _ => None,
        }
    }

    pub fn label(&self) -> &'static str {
        match self {
            Self::Fetch { .. } => "Fetch",
            Self::Pull { .. } => "Pull",
            Self::Push { .. } => "Push",
        }
    }

    pub fn pending_label(&self) -> &'static str {
        match self {
            Self::Fetch { .. } => "Fetching…",
            Self::Pull { .. } => "Pulling…",
            Self::Push { .. } => "Pushing…",
        }
    }

    fn run(&self, root: &std::path::Path) -> Result<GitSyncReport, omaterm_context::GitError> {
        match self {
            Self::Fetch { remote } => omaterm_context::git_fetch(root, remote.as_deref()),
            Self::Pull { remote } => omaterm_context::git_pull(root, remote.as_deref()),
            Self::Push { set_upstream } => omaterm_context::git_push(root, *set_upstream),
        }
    }
}

pub struct PendingSync {
    pub project: ProjectId,
    pub operation: Operation,
    pub started: Instant,
    receiver: Receiver<Result<GitSyncReport, String>>,
    worker: Option<std::thread::JoinHandle<()>>,
}

pub struct Completion {
    pub project: ProjectId,
    pub operation: Operation,
    pub result: Result<GitSyncReport, String>,
}

impl PendingSync {
    pub fn start(
        project: ProjectId,
        operation: Operation,
        pinned: Option<PathBuf>,
        active_cwd: Option<PathBuf>,
    ) -> Result<Self, std::io::Error> {
        let (sender, receiver) = mpsc::channel();
        let worker_operation = operation.clone();
        let worker = std::thread::Builder::new()
            .name("git-sync".into())
            .spawn(move || {
                let resolved =
                    omaterm_context::resolve_root(pinned.as_deref(), active_cwd.as_deref());
                let result = match resolved.root {
                    None => Err("no project root".to_owned()),
                    Some(root) => worker_operation
                        .run(&root)
                        .map_err(|error| error.to_string()),
                };
                let _ = sender.send(result);
            })?;
        Ok(Self {
            project,
            operation,
            started: Instant::now(),
            receiver,
            worker: Some(worker),
        })
    }

    /// Transfers the bounded worker to the asynchronous application teardown.
    pub fn take_shutdown_thread(self) -> Option<std::thread::JoinHandle<()>> {
        self.worker
    }

    pub fn take_completion(pending: &mut Option<Self>) -> Option<Completion> {
        let task = pending.as_ref()?;
        if task
            .worker
            .as_ref()
            .is_some_and(|worker| !worker.is_finished())
        {
            return None;
        }
        let mut result = match task.receiver.try_recv() {
            Ok(result) => result,
            Err(TryRecvError::Empty) => return None,
            Err(TryRecvError::Disconnected) => {
                Err("Git worker stopped before reporting a result. Retry the operation.".into())
            }
        };
        let task = pending.take()?;
        if let Some(worker) = task.worker
            && worker.join().is_err()
        {
            result =
                Err("Git worker stopped before reporting a result. Retry the operation.".into());
        }
        Some(Completion {
            project: task.project,
            operation: task.operation,
            result,
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn started_worker_remains_owned_for_shutdown() {
        let pending = PendingSync::start(
            ProjectId::new(),
            Operation::Fetch { remote: None },
            None,
            None,
        )
        .unwrap();
        let worker = pending
            .take_shutdown_thread()
            .expect("started Git worker must remain owned until joined");
        worker
            .join()
            .expect("rootless worker should finish normally");
    }

    #[test]
    fn shutdown_joins_worker_before_reporting_completion() {
        use std::time::Duration;
        let (sender, receiver) = mpsc::channel();
        let (release, released) = mpsc::channel();
        let (exited, exit_observed) = mpsc::channel();
        let worker = std::thread::spawn(move || {
            released.recv().unwrap();
            let _ = sender.send(Err("cancelled fixture".into()));
            exited.send(()).unwrap();
        });
        let pending = PendingSync {
            project: ProjectId::new(),
            operation: Operation::Fetch { remote: None },
            started: Instant::now(),
            receiver,
            worker: Some(worker),
        };
        let (joined, join_observed) = mpsc::channel();
        let teardown = std::thread::spawn(move || {
            pending.take_shutdown_thread().unwrap().join().unwrap();
            joined.send(()).unwrap();
        });
        assert!(
            join_observed
                .recv_timeout(Duration::from_millis(30))
                .is_err(),
            "shutdown cannot finish while Git still runs"
        );
        release.send(()).unwrap();
        join_observed.recv_timeout(Duration::from_secs(2)).unwrap();
        exit_observed
            .try_recv()
            .expect("Git worker exited before teardown completed");
        teardown.join().unwrap();
    }

    #[test]
    fn completion_waits_for_worker_exit_without_blocking_ui() {
        use std::time::Duration;
        let (sender, receiver) = mpsc::channel();
        let (ready, ready_rx) = mpsc::channel();
        let (release, released) = mpsc::channel();
        let worker = std::thread::spawn(move || {
            sender
                .send(Ok(GitSyncReport {
                    operation: "fetch",
                    detail: "complete".into(),
                }))
                .unwrap();
            ready.send(()).unwrap();
            released.recv().unwrap();
        });
        let mut pending = Some(PendingSync {
            project: ProjectId::new(),
            operation: Operation::Fetch { remote: None },
            started: Instant::now(),
            receiver,
            worker: Some(worker),
        });
        ready_rx.recv_timeout(Duration::from_secs(2)).unwrap();
        assert!(PendingSync::take_completion(&mut pending).is_none());
        assert!(
            pending.is_some(),
            "a sent result cannot detach a still-running worker"
        );
        release.send(()).unwrap();
        let deadline = Instant::now() + Duration::from_secs(2);
        let completion = loop {
            if let Some(completion) = PendingSync::take_completion(&mut pending) {
                break completion;
            }
            assert!(Instant::now() < deadline);
            std::thread::yield_now();
        };
        assert_eq!(completion.result.unwrap().detail, "complete");
        assert!(pending.is_none());
    }

    #[test]
    fn panicked_owned_worker_reports_failure_and_clears_pending() {
        let (sender, receiver) = mpsc::channel();
        let worker = std::thread::spawn(move || {
            drop(sender);
            panic!("fixture worker panic");
        });
        while !worker.is_finished() {
            std::thread::yield_now();
        }
        let mut pending = Some(PendingSync {
            project: ProjectId::new(),
            operation: Operation::Fetch { remote: None },
            started: Instant::now(),
            receiver,
            worker: Some(worker),
        });
        let completion = PendingSync::take_completion(&mut pending).unwrap();
        assert!(completion.result.unwrap_err().contains("worker stopped"));
        assert!(pending.is_none());
    }

    #[test]
    fn disconnected_worker_clears_busy_and_reports_failure() {
        let (sender, receiver) = mpsc::channel();
        let project = ProjectId::new();
        let mut pending = Some(PendingSync {
            project,
            operation: Operation::Push {
                set_upstream: false,
            },
            started: Instant::now(),
            receiver,
            worker: None,
        });
        drop(sender);
        let completion =
            PendingSync::take_completion(&mut pending).expect("worker disconnect must finish Push");
        assert_eq!(completion.project, project);
        assert!(completion.result.unwrap_err().contains("worker stopped"));
        assert!(pending.is_none(), "Git buttons must be usable again");
    }

    #[test]
    fn pending_then_success_keeps_original_project_and_report() {
        let (sender, receiver) = mpsc::channel();
        let origin = ProjectId::new();
        let selected_after_switch = ProjectId::new();
        let mut pending = Some(PendingSync {
            project: origin,
            operation: Operation::Pull { remote: None },
            started: Instant::now(),
            receiver,
            worker: None,
        });
        assert!(PendingSync::take_completion(&mut pending).is_none());
        assert!(pending.is_some());
        sender
            .send(Ok(GitSyncReport {
                operation: "pull",
                detail: "already up to date".into(),
            }))
            .unwrap();
        let completion = PendingSync::take_completion(&mut pending).unwrap();
        assert_eq!(completion.project, origin);
        assert_ne!(completion.project, selected_after_switch);
        assert_eq!(completion.result.unwrap().detail, "already up to date");
        assert!(pending.is_none());
        assert!(PendingSync::take_completion(&mut pending).is_none());
    }

    #[test]
    fn worker_failure_clears_pending_and_allows_retry() {
        let (sender, receiver) = mpsc::channel();
        let mut pending = Some(PendingSync {
            project: ProjectId::new(),
            operation: Operation::Push {
                set_upstream: false,
            },
            started: Instant::now(),
            receiver,
            worker: None,
        });
        sender.send(Err("git operation timed out".into())).unwrap();
        let completion = PendingSync::take_completion(&mut pending).unwrap();
        assert_eq!(completion.result.unwrap_err(), "git operation timed out");
        assert!(pending.is_none());
    }
}

#[cfg(test)]
mod worker_tests {
    use super::*;
    use std::time::Duration;

    struct Repo(PathBuf);

    impl Repo {
        fn new() -> Self {
            let root =
                std::env::temp_dir().join(format!("omaterm-sync-ui-{}", uuid::Uuid::new_v4()));
            std::fs::create_dir_all(&root).unwrap();
            let repo = Self(root);
            repo.git(&["init", "--quiet", "-b", "main"]);
            repo.git(&["config", "user.name", "OmaTerm test"]);
            repo.git(&["config", "user.email", "test@example.invalid"]);
            repo.git(&["config", "commit.gpgsign", "false"]);
            repo.git(&["commit", "--quiet", "--allow-empty", "-m", "initial"]);
            repo
        }

        fn git(&self, args: &[&str]) {
            let output = std::process::Command::new("git")
                .args(args)
                .current_dir(&self.0)
                .output()
                .unwrap();
            assert!(
                output.status.success(),
                "git {args:?}: {}",
                String::from_utf8_lossy(&output.stderr)
            );
        }
    }

    impl Drop for Repo {
        fn drop(&mut self) {
            let _ = std::fs::remove_dir_all(&self.0);
        }
    }

    fn wait(pending: &mut Option<PendingSync>) -> Completion {
        let deadline = Instant::now() + Duration::from_secs(10);
        loop {
            if let Some(completion) = PendingSync::take_completion(pending) {
                return completion;
            }
            assert!(Instant::now() < deadline, "Git worker did not complete");
            std::thread::sleep(Duration::from_millis(5));
        }
    }

    #[test]
    fn real_push_error_recovers_then_publish_succeeds_on_local_remote() {
        let repo = Repo::new();
        let remote = repo.0.join("remote.git");
        repo.git(&["init", "--bare", "--quiet", remote.to_str().unwrap()]);
        repo.git(&["remote", "add", "origin", remote.to_str().unwrap()]);
        let project = ProjectId::new();
        let mut pending = Some(
            PendingSync::start(
                project,
                Operation::Push {
                    set_upstream: false,
                },
                Some(repo.0.clone()),
                None,
            )
            .unwrap(),
        );
        let failure = wait(&mut pending);
        assert!(failure.result.unwrap_err().contains("upstream"));
        assert!(pending.is_none());
        let (target, operation) = Operation::from_command(GitCommand::SyncPush {
            project,
            set_upstream: true,
        })
        .unwrap();
        pending = Some(PendingSync::start(target, operation, Some(repo.0.clone()), None).unwrap());
        let completion = wait(&mut pending);
        assert_eq!(completion.project, project);
        assert_eq!(
            completion.result.unwrap().detail,
            "published main to origin"
        );
        assert!(pending.is_none());
        let output = std::process::Command::new("git")
            .arg("--git-dir")
            .arg(&remote)
            .args(["rev-parse", "refs/heads/main"])
            .output()
            .unwrap();
        assert!(
            output.status.success(),
            "local remote must contain pushed main"
        );
    }

    #[test]
    fn fetch_preserves_explicit_remote_and_surfaces_error() {
        let repo = Repo::new();
        let project = ProjectId::new();
        let (target, operation) = Operation::from_command(GitCommand::SyncFetch {
            project,
            remote: Some("missing-ui-remote".into()),
        })
        .unwrap();
        let mut pending =
            Some(PendingSync::start(target, operation, Some(repo.0.clone()), None).unwrap());
        let completion = wait(&mut pending);
        assert!(completion.result.unwrap_err().contains("missing-ui-remote"));
        assert!(pending.is_none());
    }
}
