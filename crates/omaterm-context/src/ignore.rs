//! Ignore policy: `.gitignore` + `.ignore` semantics via the `ignore`
//! crate (M12 spike: 0.4.33, Unlicense OR MIT — see `docs/dependencies.md`).
//!
//! `show_hidden` toggles dotfile inclusion; ignore files are always
//! respected. Symlinks are never followed by the walker, so link escapes
//! stay rejected by [`crate::canonicalize_under_root`].

/// Filesystem listing policy for one project root.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct IgnoreFilter {
    show_hidden: bool,
}

impl IgnoreFilter {
    pub fn new(show_hidden: bool) -> Self {
        Self { show_hidden }
    }

    pub fn show_hidden(&self) -> bool {
        self.show_hidden
    }

    /// Dotfile check for a single path (file-name based, no filesystem
    /// access). The full walker below applies this plus ignore files.
    pub fn is_visible_name(&self, path: &std::path::Path) -> bool {
        if self.show_hidden {
            return true;
        }
        !path
            .file_name()
            .is_some_and(|name| name.to_string_lossy().starts_with('.'))
    }

    /// Standard walker over a resolved root: gitignore, `.ignore`, parent
    /// ignore files, and global git excludes all apply; hidden filtering
    /// follows the policy; symlinked directories are not descended into.
    ///
    /// `require_git(false)` is deliberate: a pinned non-repo directory still
    /// has a root, and its `.gitignore` expresses the same listing intent as
    /// inside a repository.
    pub fn walker(&self, root: &std::path::Path) -> ignore::Walk {
        self.builder(root).build()
    }

    /// Same policy as [`Self::walker`] as a builder so callers can bound
    /// depth (single-level `list_dir`) without duplicating the options.
    pub fn builder(&self, root: &std::path::Path) -> ignore::WalkBuilder {
        let mut builder = ignore::WalkBuilder::new(root);
        builder
            .hidden(!self.show_hidden)
            .git_ignore(true)
            .ignore(true)
            .parents(true)
            .git_global(true)
            .git_exclude(true)
            .require_git(false)
            .follow_links(false);
        builder
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn fixture_tree(name: &str) -> std::path::PathBuf {
        let root =
            std::env::temp_dir().join(format!("omaterm-m12-ignore-{}-{name}", std::process::id()));
        let _ = std::fs::remove_dir_all(&root);
        std::fs::create_dir_all(root.join("src")).unwrap();
        std::fs::write(root.join("src").join("main.rs"), b"fn main() {}").unwrap();
        std::fs::write(root.join("src").join("ignored.log"), b"x").unwrap();
        std::fs::write(root.join(".hidden"), b"x").unwrap();
        std::fs::write(root.join(".gitignore"), b"*.log\n").unwrap();
        std::fs::write(root.join(".ignore"), b"extra.txt\n").unwrap();
        std::fs::write(root.join("extra.txt"), b"x").unwrap();
        root
    }

    fn walk_names(filter: &IgnoreFilter, root: &std::path::Path) -> Vec<String> {
        let mut names: Vec<String> = filter
            .walker(root)
            .filter_map(|entry| entry.ok())
            .filter(|entry| entry.path() != root)
            .map(|entry| {
                entry
                    .path()
                    .strip_prefix(root)
                    .unwrap()
                    .to_string_lossy()
                    .into_owned()
            })
            .collect();
        names.sort();
        names
    }

    #[test]
    fn hidden_and_ignored_files_follow_the_policy() {
        let root = fixture_tree("policy");
        let listed = walk_names(&IgnoreFilter::new(false), &root);
        assert!(listed.contains(&"src/main.rs".to_owned()), "{listed:?}");
        assert!(!listed.iter().any(|n| n == ".hidden"), "{listed:?}");
        assert!(
            !listed.iter().any(|n| n.ends_with("ignored.log")),
            "{listed:?}"
        );
        assert!(!listed.iter().any(|n| n == "extra.txt"), "{listed:?}");

        let shown = walk_names(&IgnoreFilter::new(true), &root);
        assert!(shown.contains(&".hidden".to_owned()), "{shown:?}");
        // Ignore files still apply when hidden files are shown.
        assert!(
            !shown.iter().any(|n| n.ends_with("ignored.log")),
            "{shown:?}"
        );
        assert!(!shown.iter().any(|n| n == "extra.txt"), "{shown:?}");
        let _ = std::fs::remove_dir_all(&root);
    }

    #[test]
    fn single_name_visibility_matrix() {
        let hidden = IgnoreFilter::new(false);
        assert!(hidden.is_visible_name(std::path::Path::new("src/main.rs")));
        assert!(!hidden.is_visible_name(std::path::Path::new(".hidden")));
        assert!(!hidden.is_visible_name(std::path::Path::new("src/.keep")));
        let shown = IgnoreFilter::new(true);
        assert!(shown.is_visible_name(std::path::Path::new(".hidden")));
        assert!(shown.show_hidden());
        assert!(!hidden.show_hidden());
    }
}
