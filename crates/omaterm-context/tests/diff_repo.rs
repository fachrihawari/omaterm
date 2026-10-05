//! Real-repository integration for M15 unified diff (blueprint §33,
//! system git only).
//!
//! Requires the system git binary; a missing binary fails loudly rather
//! than passing vacuously. Repos live under `/tmp` and are removed after
//! each test.

use std::path::{Path, PathBuf};
use std::process::{Command, Stdio};

use omaterm_context::{DiffRequest, MAX_DIFF_FILES, git_diff, git_stage_hunk};

fn git(repo: &Path, args: &[&str]) -> String {
    let output = Command::new("git")
        .args(args)
        .current_dir(repo)
        .env("GIT_TERMINAL_PROMPT", "0")
        .env("GIT_CONFIG_NOSYSTEM", "1")
        .stdin(Stdio::null())
        .output()
        .expect("git must spawn");
    assert!(
        output.status.success(),
        "git {args:?} in {}: {}",
        repo.display(),
        String::from_utf8_lossy(&output.stderr)
    );
    String::from_utf8_lossy(&output.stdout).into_owned()
}

fn fixture(name: &str) -> PathBuf {
    assert!(
        Command::new("git")
            .arg("--version")
            .stdin(Stdio::null())
            .stdout(Stdio::null())
            .stderr(Stdio::null())
            .status()
            .is_ok_and(|status| status.success()),
        "system git is required for this test"
    );
    let repo: PathBuf =
        std::env::temp_dir().join(format!("omaterm-m15-diff-{name}-{}", std::process::id()));
    let _ = std::fs::remove_dir_all(&repo);
    std::fs::create_dir_all(&repo).unwrap();
    git(&repo, &["init"]);
    git(&repo, &["config", "user.email", "m15@test"]);
    git(&repo, &["config", "user.name", "m15"]);
    git(&repo, &["config", "commit.gpgsign", "false"]);
    repo
}

fn show_request(staged: bool, path: Option<&str>) -> DiffRequest {
    DiffRequest {
        staged,
        path: path.map(PathBuf::from),
        context_lines: 3,
        files_only: false,
        untracked: false,
    }
}

#[test]
fn unstaged_and_staged_sides_agree_with_git_cli() {
    let repo = fixture("sides");
    std::fs::write(repo.join("tracked.txt"), b"one\ntwo\nthree\n").unwrap();
    std::fs::write(repo.join("other.txt"), b"alpha\n").unwrap();
    git(&repo, &["add", "-A"]);
    git(&repo, &["commit", "-qm", "init"]);
    // Unstaged edit plus a staged edit of a second file.
    std::fs::write(repo.join("tracked.txt"), b"one\nTWO\nthree\n").unwrap();
    std::fs::write(repo.join("other.txt"), b"alpha\nbeta\n").unwrap();
    git(&repo, &["add", "other.txt"]);

    let unstaged = git_diff(&repo, &show_request(false, None)).unwrap();
    assert!(!unstaged.staged && !unstaged.truncated);
    assert_eq!(unstaged.files.len(), 1);
    assert_eq!(unstaged.files[0].path, PathBuf::from("tracked.txt"));
    let body: Vec<String> = unstaged.files[0].hunks[0]
        .lines
        .iter()
        .map(|line| format!("{}:{}", line.kind.as_str(), line.text))
        .collect();
    assert!(body.contains(&"deletion:two".to_owned()));
    assert!(body.contains(&"addition:TWO".to_owned()));

    let staged = git_diff(&repo, &show_request(true, None)).unwrap();
    assert!(staged.staged);
    assert_eq!(staged.files.len(), 1);
    assert_eq!(staged.files[0].path, PathBuf::from("other.txt"));

    // Ground truth: the CLI's own file lists match both sides.
    let unstaged_cli = git(&repo, &["diff", "--name-only", "--", "."]);
    assert_eq!(unstaged_cli.trim(), "tracked.txt");
    let staged_cli = git(&repo, &["diff", "--cached", "--name-only", "--", "."]);
    assert_eq!(staged_cli.trim(), "other.txt");

    // Single-path filter narrows to the requested file on either side.
    let filtered = git_diff(&repo, &show_request(false, Some("tracked.txt"))).unwrap();
    assert_eq!(filtered.files.len(), 1);
    let empty = git_diff(&repo, &show_request(false, Some("other.txt"))).unwrap();
    assert!(empty.files.is_empty() && !empty.truncated);
    let _ = std::fs::remove_dir_all(&repo);
}

#[test]
fn path_traversal_is_rejected_before_git_runs() {
    let repo = fixture("traversal");
    std::fs::write(repo.join("a.txt"), b"hi\n").unwrap();
    git(&repo, &["add", "-A"]);
    git(&repo, &["commit", "-qm", "init"]);
    let error = git_diff(&repo, &show_request(false, Some("../outside.txt"))).unwrap_err();
    assert_eq!(error.code(), "path_outside_root");
    let _ = std::fs::remove_dir_all(&repo);
}

#[test]
fn path_filter_treats_git_pathspec_magic_as_a_literal_filename() {
    let repo = fixture("literal-pathspec");
    let path = ":(top,literal)draft.txt";
    std::fs::write(repo.join(path), b"before\n").unwrap();
    git(&repo, &["add", "-A"]);
    git(&repo, &["commit", "-qm", "init"]);
    std::fs::write(repo.join(path), b"after\n").unwrap();

    let info = git_diff(&repo, &show_request(false, Some(path))).unwrap();
    assert_eq!(info.files.len(), 1);
    assert_eq!(info.files[0].path, PathBuf::from(path));
    let _ = std::fs::remove_dir_all(&repo);
}

#[test]
fn stage_hunk_updates_only_the_selected_index_change() {
    let repo = fixture("stage-hunk");
    let original = (1..=20)
        .map(|line| format!("line {line}\n"))
        .collect::<String>();
    std::fs::write(repo.join("tracked.txt"), &original).unwrap();
    git(&repo, &["add", "-A"]);
    git(&repo, &["commit", "-qm", "init"]);
    let changed = original
        .replace("line 2\n", "line two\n")
        .replace("line 18\n", "line eighteen\n");
    std::fs::write(repo.join("tracked.txt"), changed).unwrap();

    let before = git_diff(&repo, &show_request(false, Some("tracked.txt"))).unwrap();
    assert_eq!(before.files[0].hunks.len(), 2);
    let worktree_before = std::fs::read(repo.join("tracked.txt")).unwrap();
    git_stage_hunk(&repo, Path::new("tracked.txt"), before.files[0].hunks[0].id).unwrap();
    assert_eq!(
        std::fs::read(repo.join("tracked.txt")).unwrap(),
        worktree_before
    );
    let index_before_retry = git(&repo, &["show", ":tracked.txt"]);
    assert!(git_stage_hunk(&repo, Path::new("tracked.txt"), before.files[0].hunks[0].id).is_err());
    assert_eq!(git(&repo, &["show", ":tracked.txt"]), index_before_retry);

    let cached = git(&repo, &["diff", "--cached", "--", "tracked.txt"]);
    assert!(cached.contains("-line 2\n") && cached.contains("+line two\n"));
    assert!(!cached.contains("line eighteen"));
    let unstaged = git(&repo, &["diff", "--", "tracked.txt"]);
    assert!(unstaged.contains("-line 18\n") && unstaged.contains("+line eighteen\n"));
    assert!(!unstaged.contains("line two"));
    let _ = std::fs::remove_dir_all(&repo);
}

#[test]
fn staged_and_unstaged_diffs_on_plain_dirs_are_not_a_repo() {
    // Outside a work tree git reinterprets `diff` as `--no-index` and
    // rejects `--cached`: both sides must still surface `not_a_repo`
    // (empty envelope upstream), never `git_failed`.
    let plain: PathBuf =
        std::env::temp_dir().join(format!("omaterm-m15-plain-{}", std::process::id()));
    let _ = std::fs::remove_dir_all(&plain);
    std::fs::create_dir_all(&plain).unwrap();
    for staged in [false, true] {
        let error = git_diff(&plain, &show_request(staged, None)).unwrap_err();
        assert_eq!(error.code(), "not_a_repo", "staged={staged}");
    }
    let _ = std::fs::remove_dir_all(&plain);
}

#[test]
fn thousand_file_diff_hits_caps_with_a_valid_envelope() {
    let repo = fixture("thousand");
    for index in 0..MAX_DIFF_FILES + 50 {
        std::fs::write(repo.join(format!("f{index:04}.txt")), b"v1\n").unwrap();
    }
    git(&repo, &["add", "-A"]);
    git(&repo, &["commit", "-qm", "init"]);
    for index in 0..MAX_DIFF_FILES + 50 {
        std::fs::write(repo.join(format!("f{index:04}.txt")), b"v2\n").unwrap();
    }
    // Full bodies: capped file count, accurate flag.
    let full = git_diff(&repo, &show_request(false, None)).unwrap();
    assert_eq!(full.files.len(), MAX_DIFF_FILES);
    assert!(full.truncated);
    assert!(
        full.files
            .iter()
            .all(|file| !file.hunks.is_empty() && !file.binary)
    );
    // Header-only surface: same cap, hunk counts without bodies.
    let headers = git_diff(
        &repo,
        &DiffRequest {
            files_only: true,
            ..show_request(false, None)
        },
    )
    .unwrap();
    assert_eq!(headers.files.len(), MAX_DIFF_FILES);
    assert!(headers.truncated);
    assert!(headers.files.iter().all(|file| file.hunks.is_empty()));
    assert!(headers.files.iter().all(|file| file.hunk_count >= 1));
    let _ = std::fs::remove_dir_all(&repo);
}

#[test]
fn binary_and_rename_shapes_parse_live() {
    let repo = fixture("shapes");
    std::fs::write(repo.join("notes.txt"), b"a\nb\n").unwrap();
    std::fs::write(repo.join("blob.bin"), [0u8, 159, 22, 0, 255]).unwrap();
    git(&repo, &["add", "-A"]);
    git(&repo, &["commit", "-qm", "init"]);
    git(&repo, &["mv", "notes.txt", "renamed.txt"]);
    std::fs::write(repo.join("blob.bin"), [0u8, 159, 22, 0, 254]).unwrap();
    // `git mv` stages the rename: it reads on the staged side, while the
    // binary edit stays unstaged.
    let staged = git_diff(&repo, &show_request(true, None)).unwrap();
    assert!(!staged.truncated);
    let renamed = staged
        .files
        .iter()
        .find(|file| file.path.as_path() == Path::new("renamed.txt"))
        .expect("rename must parse");
    assert!(matches!(
        renamed.status,
        omaterm_core::DiffFileStatus::Renamed
    ));
    let unstaged = git_diff(&repo, &show_request(false, None)).unwrap();
    let binary = unstaged
        .files
        .iter()
        .find(|file| file.path.as_path() == Path::new("blob.bin"))
        .expect("binary change must parse");
    assert!(binary.binary && binary.hunks.is_empty());
    let _ = std::fs::remove_dir_all(&repo);
}

#[test]
fn untracked_file_renders_as_all_additions() {
    let repo = fixture("untracked");
    std::fs::write(repo.join("tracked.txt"), b"base\n").unwrap();
    git(&repo, &["add", "-A"]);
    git(&repo, &["commit", "-qm", "init"]);
    std::fs::write(repo.join("new.txt"), b"one\ntwo\n").unwrap();
    // The plain index diff never lists untracked files.
    let plain = git_diff(&repo, &show_request(false, Some("new.txt"))).unwrap();
    assert!(plain.files.is_empty());
    // The untracked request renders empty → content, like VSCode.
    let request = DiffRequest {
        untracked: true,
        ..show_request(false, Some("new.txt"))
    };
    let info = git_diff(&repo, &request).unwrap();
    assert!(!info.staged && !info.truncated);
    assert_eq!(info.files.len(), 1);
    let file = &info.files[0];
    assert_eq!(file.path, PathBuf::from("new.txt"));
    assert!(matches!(file.status, omaterm_core::DiffFileStatus::Added));
    assert!(!file.binary && !file.hunks.is_empty());
    assert!(
        file.hunks
            .iter()
            .flat_map(|hunk| &hunk.lines)
            .all(|line| matches!(line.kind, omaterm_core::DiffLineKind::Addition))
    );
    let texts: Vec<&str> = info.files[0].hunks[0]
        .lines
        .iter()
        .map(|line| line.text.as_str())
        .collect();
    assert_eq!(texts, vec!["one", "two"]);
    let _ = std::fs::remove_dir_all(&repo);
}
