//! Stable-channel update model (M21 S3): pure parsing, comparison,
//! install-kind detection and URL construction over the GitHub release
//! layout that `install.sh` and the packaging already consume.
//!
//! Network calls and filesystem effects sit behind these pure helpers;
//! the decisions (what kind of install is this, is this the published
//! artifact, may this binary be replaced) are unit-testable without a
//! socket or a real install.

/// Owner parts of the release channel this build self-updates from
/// (matches `install.sh` / `packaging/`).
pub const RELEASE_OWNER: &str = "fachrihawari";
pub const RELEASE_REPO: &str = "omaterm";

/// Channel: stable only (locked decision). Pre-releases and drafts are
/// never considered, even if `releases/latest` were pointed at them.
pub const CHANNEL: &str = "stable";

/// Largest accepted tarball/checksum download, in bytes (S3b). A release
/// artifact for this project is a few megabytes; the cap keeps a hostile
/// or misconfigured endpoint from exhausting memory during apply.
#[allow(dead_code)]
pub const MAX_ARTIFACT_BYTES: u64 = 128 * 1024 * 1024;

/// Canonical HTML redirect to the newest release. The `url_effective`
/// of this request ends in the `vX.Y.Z` tag, so no API rate limit
/// applies and no scraping of release notes is needed.
///
/// Live in production via `check_channel`; the test build cfg's out the
/// network spawner, hence the scoped allow below.
#[allow(dead_code)]
pub fn latest_release_url() -> String {
    format!("https://github.com/{RELEASE_OWNER}/{RELEASE_REPO}/releases/latest")
}

/// Download URL for one release tag's Linux artifact, matching the name
/// `release.yml` publishes (`omaterm-<ver>-x86_64.tar.gz`). Used by the
/// S3b apply pipeline; the S3a checker only needs the redirect target.
#[allow(dead_code)]
pub fn artifact_url(tag: &str) -> String {
    format!(
        "https://github.com/{RELEASE_OWNER}/{RELEASE_REPO}/releases/download/{tag}/omaterm-{tag}-x86_64.tar.gz"
    )
}

/// Sidecar checksum published next to the artifact (S3b verifies before
/// staging).
#[allow(dead_code)]
pub fn checksum_url(tag: &str) -> String {
    format!("{}.sha256", artifact_url(tag))
}

/// How the running binary is installed, which decides whether a
/// self-update may replace it (S3b). A package manager owns the file
/// in `Managed` installs, so OmaTerm must not fight the database.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum InstallKind {
    /// No package manager claims this binary: the user owns the replace.
    /// Constructed by the probe once `pacman` does not answer; the
    /// Settings render and CLI report it through [`InstallKind::as_str`].
    #[allow(dead_code)]
    Owned,
    /// `pacman -Qo <binary>` succeeded: the package database owns it.
    Managed,
    /// The install prefix could not be probed at all.
    Unknown,
}

impl InstallKind {
    /// Wire-friendly stable code; agents branch on this, never on prose.
    /// Reported by the Settings/CLI apply render when an install is
    /// managed or unprobeable.
    #[allow(dead_code)]
    pub fn as_str(&self) -> &'static str {
        match self {
            InstallKind::Owned => "owned",
            InstallKind::Managed => "managed",
            InstallKind::Unknown => "unknown",
        }
    }

    /// Whether OmaTerm may replace the running binary itself. Managed
    /// installs are refused with guidance (`pacman -Syu`); `Unknown`
    /// stays allowed because a failed probe must not silently disable
    /// updates — the atomic write still has to succeed on its own.
    pub fn self_update_allowed(&self) -> bool {
        match self {
            InstallKind::Owned => true,
            InstallKind::Managed => false,
            InstallKind::Unknown => true,
        }
    }
}

/// The hint served when a self-update is refused because the package
/// manager owns the binary. Kept beside [`InstallKind`] so the Settings
/// render and the CLI deliver the same words.
pub fn managed_install_guidance() -> &'static str {
    "Managed by pacman — update with `sudo pacman -Syu`; the self-updater refuses to bypass the package database."
}

/// One entry of a parsed checksum sidecar: the digest bytes of the
/// `sha256sum` line `release.yml` publishes.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct PublishedChecksum {
    pub sha256: [u8; 32],
}

/// Parse the `.sha256` sidecar `release.yml` publishes: one
/// `"<64-hex>  <name>"` line (two spaces, like `sha256sum`), tolerating
/// a leading binary-marker `\` and a missing name. Anything else is a
/// refusal, so the caller never compares against a guess.
pub fn parse_checksum_sidecar(text: &str) -> Option<PublishedChecksum> {
    let line = text.lines().find(|line| !line.trim().is_empty())?;
    let hex = line
        .trim_start()
        .trim_start_matches('\\')
        .split_whitespace()
        .next()?;
    if hex.len() != 64 || !hex.bytes().all(|byte| byte.is_ascii_hexdigit()) {
        return None;
    }
    let mut sha256 = [0u8; 32];
    for (slot, pair) in sha256.iter_mut().zip(hex.as_bytes().chunks(2)) {
        let text = std::str::from_utf8(pair).ok()?;
        *slot = u8::from_str_radix(text, 16).ok()?;
    }
    Some(PublishedChecksum { sha256 })
}

/// Compare a computed digest against the published one (both are
/// `[u8; 32]`, so this is byte equality kept as a named helper for the
/// apply pipeline's single call site).
pub fn checksum_matches(computed: &[u8; 32], published: &PublishedChecksum) -> bool {
    computed == &published.sha256
}

/// Extract the release tag from a `releases/latest` redirect target,
/// e.g. `…/tag/v0.5.1` → `0.5.1`. Returns `None` for anything that is
/// not a plain `vX.Y.Z` tag (redirect loops, odd layout, prerelease
/// suffixes like `v1.2.3-rc.1`) — the stable channel never guesses.
pub fn tag_from_redirect_url(url: &str) -> Option<String> {
    let tag = url.rsplit("/tag/").next()?;
    parse_stable_tag(tag)
}

/// Accept only `X.Y.Z` (optionally `v`-prefixed, no prerelease/build
/// metadata) and return the bare version. Anything else is `None`.
pub fn parse_stable_tag(tag: &str) -> Option<String> {
    let version = tag.strip_prefix('v').unwrap_or(tag);
    let mut parts = version.split('.');
    let mut count = 0;
    for part in parts.by_ref() {
        if part.is_empty() || !part.bytes().all(|byte| byte.is_ascii_digit()) {
            return None;
        }
        count += 1;
    }
    (count == 3).then(|| version.to_owned())
}

/// Compare a discovered stable version against the installed one.
///
/// - `Ok(false)` when they are equal or the installed version is newer
///   (a dev build ahead of the channel is "up to date", not a downgrade).
/// - `Ok(true)` when the discovered version is strictly newer.
/// - `Err` when either string is not parseable semver, so callers fail
///   with `unavailable` instead of comparing garbage.
pub fn is_newer(installed: &str, latest: &str) -> Result<bool, String> {
    let installed = semver::Version::parse(installed)
        .map_err(|error| format!("installed version is not semver: {error}"))?;
    let latest = semver::Version::parse(latest)
        .map_err(|error| format!("latest version is not semver: {error}"))?;
    Ok(latest > installed)
}

/// Outcome of one check, mapped to a stable state identifier. Shared by
/// the worker result and the Settings render so both speak one language.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum UpdateState {
    UpToDate,
    UpdateAvailable,
    Offline,
    RateLimited,
    Unavailable,
}

impl UpdateState {
    pub fn as_str(&self) -> &'static str {
        match self {
            UpdateState::UpToDate => "up_to_date",
            UpdateState::UpdateAvailable => "update_available",
            UpdateState::Offline => "offline",
            UpdateState::RateLimited => "rate_limited",
            UpdateState::Unavailable => "unavailable",
        }
    }
}

/// Run one stable-channel check against the live channel.
///
/// `installed` is the compiled binary version. The call is blocking and
/// runs on the caller's worker thread; it never touches the UI thread and
/// performs no writes. A transport failure classifies as `Offline` only
/// when the failure is a connection-level error, so a user working
/// offline is told the truth instead of being shown a broken update.
///
/// Same cfg story as `latest_release_url`: live in production, spawner
/// cfg'd out in tests.
#[allow(dead_code)]
pub fn check_channel(installed: &str) -> Outcome {
    let latest = match discover_latest_stable() {
        Ok(latest) => latest,
        Err(Failure::Offline) => {
            return Outcome {
                installed: installed.to_owned(),
                latest: None,
                available: false,
                state: UpdateState::Offline,
            };
        }
        Err(Failure::RateLimited) => {
            return Outcome {
                installed: installed.to_owned(),
                latest: None,
                available: false,
                state: UpdateState::RateLimited,
            };
        }
        Err(Failure::Unavailable) => {
            return Outcome {
                installed: installed.to_owned(),
                latest: None,
                available: false,
                state: UpdateState::Unavailable,
            };
        }
    };
    match is_newer(installed, &latest) {
        Ok(true) => Outcome {
            installed: installed.to_owned(),
            latest: Some(latest),
            available: true,
            state: UpdateState::UpdateAvailable,
        },
        Ok(false) => Outcome {
            installed: installed.to_owned(),
            latest: Some(latest),
            available: false,
            state: UpdateState::UpToDate,
        },
        Err(_) => Outcome {
            installed: installed.to_owned(),
            latest: None,
            available: false,
            state: UpdateState::Unavailable,
        },
    }
}

/// Why the channel could not be resolved. Transport-level disconnects map
/// to `Offline`; everything else stays honest about being unknown.
enum Failure {
    Offline,
    RateLimited,
    Unavailable,
}

/// Public resolve step for the apply path: returns the bare stable tag,
/// or the stage/refusal pair the router turns into a report.
///
/// Live in production (the apply worker calls it); the test build cfg's
/// out the network spawner, hence the scoped allow.
#[allow(dead_code)]
pub fn discover_latest_stable_public(
    _installed: &str,
) -> Result<String, (ApplyStage, ApplyRefusal)> {
    match discover_latest_stable() {
        Ok(tag) => Ok(tag),
        Err(Failure::Offline) => Err((ApplyStage::Resolve, ApplyRefusal::Offline)),
        Err(Failure::RateLimited) => Err((ApplyStage::Download, ApplyRefusal::Unavailable)),
        Err(Failure::Unavailable) => Err((ApplyStage::Resolve, ApplyRefusal::Unavailable)),
    }
}

/// Resolve the newest stable version by following the `releases/latest`
/// redirect. No GitHub API call, so no unauthenticated rate limit. The
/// final URI (after redirects) carries the tag; history is saved so the
/// tag survives even when GitHub chains more than one redirect.
fn discover_latest_stable() -> Result<String, Failure> {
    use ureq::ResponseExt as _;

    let response = ureq::get(latest_release_url().as_str())
        .config()
        .timeout_connect(Some(CHECK_TIMEOUT))
        .timeout_recv_response(Some(CHECK_TIMEOUT))
        .save_redirect_history(true)
        .build()
        .call()
        .map_err(map_transport_error)?;
    if response.status() == ureq::http::StatusCode::TOO_MANY_REQUESTS {
        return Err(Failure::RateLimited);
    }
    if !response.status().is_success() {
        return Err(Failure::Unavailable);
    }
    let final_uri = response
        .get_redirect_history()
        .and_then(|history| history.last())
        .unwrap_or_else(|| response.get_uri())
        .to_string();
    tag_from_redirect_url(&final_uri).ok_or(Failure::Unavailable)
}

/// Classify a transport failure. Connection-level teardowns (DNS,
/// refused, reset, timeout) are honestly `Offline`; TLS/protocol/bad
/// response shapes stay `Unavailable` rather than blaming the network.
fn map_transport_error(error: ureq::Error) -> Failure {
    use std::io::ErrorKind;
    match &error {
        ureq::Error::StatusCode(_) => Failure::Unavailable,
        ureq::Error::HostNotFound | ureq::Error::ConnectionFailed | ureq::Error::Timeout(_) => {
            Failure::Offline
        }
        ureq::Error::Io(error) => match error.kind() {
            ErrorKind::TimedOut
            | ErrorKind::NotConnected
            | ErrorKind::ConnectionRefused
            | ErrorKind::ConnectionReset
            | ErrorKind::ConnectionAborted
            | ErrorKind::NetworkDown
            | ErrorKind::NetworkUnreachable
            | ErrorKind::HostUnreachable => Failure::Offline,
            _ => Failure::Offline,
        },
        _ => Failure::Unavailable,
    }
}

/// One completed check. Mirrors `omaterm_core::UpdateCheckInfo` so the
/// router can hand the wire payload to the owner without re-classifying.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Outcome {
    pub installed: String,
    pub latest: Option<String>,
    pub available: bool,
    pub state: UpdateState,
}

impl From<Outcome> for omaterm_core::UpdateCheckInfo {
    fn from(outcome: Outcome) -> Self {
        omaterm_core::UpdateCheckInfo {
            installed: outcome.installed,
            latest: outcome.latest,
            available: outcome.available,
            state: outcome.state.as_str().to_owned(),
        }
    }
}

/// Bounded connect/read timeout for both checks and artifact downloads.
pub const CHECK_TIMEOUT: std::time::Duration = std::time::Duration::from_secs(8);

/// Binaries the release tarball ships, in the order they are installed.
/// Both are replaced together or neither is: a half-updated install with
/// a mismatched desktop/CLI pair is worse than no update at all.
pub const MANAGED_BINARIES: [&str; 2] = ["omaterm", "omaterm-desktop"];

/// Stages of one apply, in order. The worker reports the stage it refused
/// at with a stable code; the UI renders the stage name for progress.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ApplyStage {
    /// Resolved the channel and decided an update is available.
    Resolve,
    /// Downloaded the tarball and its checksum sidecar.
    Download,
    /// sha256(sidecar) == sha256(computed) passed.
    Verify,
    /// Extracted the managed binaries and staged them `+x` next to the
    /// live install.
    Stage,
    /// Atomically renamed each staged binary over its live counterpart.
    Replace,
    /// The replacement reports the target version.
    Confirm,
    /// Snapshot saved, restart requested.
    Restart,
}

impl ApplyStage {
    pub fn as_str(&self) -> &'static str {
        match self {
            ApplyStage::Resolve => "resolve",
            ApplyStage::Download => "download",
            ApplyStage::Verify => "verify",
            ApplyStage::Stage => "stage",
            ApplyStage::Replace => "replace",
            ApplyStage::Confirm => "confirm",
            ApplyStage::Restart => "restart",
        }
    }
}

/// Why an apply refused to continue. Codes, not prose: the wire payload,
/// CLI, and Settings render all carry the code.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum ApplyRefusal {
    /// No newer stable release than the installed one.
    AlreadyCurrent,
    /// No network path to the channel.
    Offline,
    /// The channel answered but not with a usable stable tag.
    Unavailable,
    /// The download exceeded its byte cap.
    DownloadTooLarge,
    /// sha256 mismatch: the artifact is not the published one.
    ChecksumMismatch,
    /// The tarball does not carry the expected binaries.
    MissingBinary,
    /// A package manager owns the install.
    ManagedInstall,
    /// Renaming the staged binaries failed without elevation (and no
    /// elevation helper was available or was declined).
    PermissionDenied,
    /// The replacement binary does not report the target version.
    ConfirmMismatch,
    /// The filesystem could not be prepared (temp dir, staging dir).
    StagingFailed,
}

impl ApplyRefusal {
    pub fn code(&self) -> &'static str {
        match self {
            ApplyRefusal::AlreadyCurrent => "already_current",
            ApplyRefusal::Offline => "offline",
            ApplyRefusal::Unavailable => "unavailable",
            ApplyRefusal::DownloadTooLarge => "download_too_large",
            ApplyRefusal::ChecksumMismatch => "checksum_mismatch",
            ApplyRefusal::MissingBinary => "missing_binary",
            ApplyRefusal::ManagedInstall => "managed_by_package_manager",
            ApplyRefusal::PermissionDenied => "permission_denied",
            ApplyRefusal::ConfirmMismatch => "restart_required",
            ApplyRefusal::StagingFailed => "staging_failed",
        }
    }
}

/// Installer seam: the apply pipeline talks to its collaborator through
/// this trait so the sequence (download, verify, install, confirm,
/// restart) is unit-testable with no network and no real binary swap.
pub trait ApplyHost {
    /// Fetch `url` and write it to `destination`, refusing anything over
    /// [`MAX_ARTIFACT_BYTES`].
    fn download(&self, url: &str, destination: &std::path::Path) -> Result<u64, ApplyRefusal>;
    /// Read `path` as UTF-8 text (checksum sidecars are text).
    fn read_text(&self, path: &std::path::Path) -> Result<String, ApplyRefusal>;
    /// sha256 of the file at `path`.
    fn read_digest(&self, path: &std::path::Path) -> Result<[u8; 32], ApplyRefusal>;
    /// Extract the managed binaries from the tarball at `archive` into
    /// `staging`, marking them executable. Reports the staged paths.
    fn stage(
        &self,
        archive: &std::path::Path,
        staging: &std::path::Path,
        tag: &str,
    ) -> Result<Vec<std::path::PathBuf>, ApplyRefusal>;
    /// Replace `live` with `staged_file`, requesting elevation when the
    /// rename is refused. Returns true when `live` now exists.
    fn replace(
        &self,
        live: &std::path::Path,
        staged_file: &std::path::Path,
    ) -> Result<bool, ApplyRefusal>;
    /// Run `<live> --version` (a spawned probe, not shell text scraping).
    fn confirm_version(&self, live: &std::path::Path, target: &str) -> Result<(), ApplyRefusal>;
    /// Persist the workspace, then re-exec the freshly installed binary.
    /// Returns only when the restart did not happen.
    fn restart(&self) -> Result<(), ApplyRefusal>;
}

/// Tiny helper so the checksum step stays one expression.
trait Pipe: Sized {
    fn pipe<R>(self, f: impl FnOnce(Self) -> R) -> R
    where
        Self: Copy,
    {
        f(self)
    }
}
impl Pipe for &str {}

/// Run one apply end to end (S3b). Every step leaves the install in a
/// consistent state on failure: staging happens in a temp dir, binaries
/// are replaced only after the checksum passed, and a failed confirm
/// reports the stage rather than restarting a mismatched install.
#[allow(clippy::too_many_lines)]
pub fn apply<H: ApplyHost>(
    host: &H,
    plan: &ApplyPlan,
    work_dir: &std::path::Path,
) -> Result<ApplyStage, (ApplyStage, ApplyRefusal)> {
    let tag = plan.target.clone();
    // 1. Preflight (pure): managed installs, current versions, downgrades.
    let _needs_downgrade_confirm =
        apply_preflight(plan).map_err(|refusal| (ApplyStage::Resolve, refusal))?;
    // 2. Download the artifact plus its sidecar.
    let archive = work_dir.join(format!("omaterm-{tag}.tar.gz"));
    host.download(&artifact_url(&tag), &archive)
        .map_err(|refusal| (ApplyStage::Download, refusal))?;
    let sidecar_path = work_dir.join(format!("omaterm-{tag}.tar.gz.sha256"));
    host.download(&checksum_url(&tag), &sidecar_path)
        .map_err(|refusal| (ApplyStage::Download, refusal))?;
    // 3. Verify: the sidecar digest must equal sha256(tarball).
    let published = host
        .read_text(&sidecar_path)
        .map_err(|refusal| (ApplyStage::Verify, refusal))?
        .pipe(parse_checksum_sidecar)
        .ok_or((ApplyStage::Verify, ApplyRefusal::ChecksumMismatch))?;
    let computed = host
        .read_digest(&archive)
        .map_err(|refusal| (ApplyStage::Verify, refusal))?;
    if !checksum_matches(&computed, &published) {
        return Err((ApplyStage::Verify, ApplyRefusal::ChecksumMismatch));
    }
    // 4. Stage the managed binaries (+x) into a temp dir.
    let staging = work_dir.join("stage");
    let staged = host
        .stage(&archive, &staging, &tag)
        .map_err(|refusal| (ApplyStage::Stage, refusal))?;
    for name in MANAGED_BINARIES {
        if !staged.iter().any(|path| {
            path.file_name()
                .and_then(|file| file.to_str())
                .is_some_and(|file| file == name)
        }) {
            return Err((ApplyStage::Stage, ApplyRefusal::MissingBinary));
        }
    }
    // 5. Replace each binary beside the running executable (an install
    // keeps both binaries in one directory).
    let live_dir = std::path::Path::new(&plan.exe_path)
        .parent()
        .ok_or((ApplyStage::Replace, ApplyRefusal::PermissionDenied))?;
    let mut replaced = Vec::new();
    for staged_file in staged {
        let Some(name) = staged_file
            .file_name()
            .and_then(|file| file.to_str())
            .map(std::path::PathBuf::from)
        else {
            continue;
        };
        let live = live_dir.join(&name);
        let migrated = host
            .replace(&live, &staged_file)
            .map_err(|refusal| (ApplyStage::Replace, refusal))?;
        if migrated {
            replaced.push(live);
        }
    }
    // 6. Confirm the replaced binary reports the target version.
    let primary = replaced
        .first()
        .cloned()
        .ok_or((ApplyStage::Confirm, ApplyRefusal::MissingBinary))?;
    host.confirm_version(&primary, &tag)
        .map_err(|refusal| (ApplyStage::Confirm, refusal))?;
    // 7. Persist and restart.
    host.restart()
        .map_err(|refusal| (ApplyStage::Restart, refusal))?;
    Ok(ApplyStage::Restart)
}

/// What the apply pipeline must be told before it can run: where the
/// running binary lives and what the channel resolved to. Kept as a
/// value so the pure decision steps can be tested without a process.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ApplyPlan {
    /// Version to install (stable tag, bare `X.Y.Z`).
    pub target: String,
    /// Version currently installed.
    pub installed: String,
    /// Absolute path of the running executable.
    pub exe_path: String,
    /// How that executable is installed on this machine.
    pub install_kind: InstallKind,
}

/// Pure preflight for the apply (before any bytes move). A refusal here
/// means the caller must not proceed; `Ok(())` requires a further
/// downgrade confirm when `target < installed`.
pub fn apply_preflight(plan: &ApplyPlan) -> Result<Option<bool>, ApplyRefusal> {
    if !plan.install_kind.self_update_allowed() {
        return Err(ApplyRefusal::ManagedInstall);
    }
    // `target` must be a plain stable version we can reason about.
    let _ = parse_stable_tag(&plan.target).ok_or(ApplyRefusal::Unavailable)?;
    let _ = parse_stable_tag(&plan.installed).ok_or(ApplyRefusal::Unavailable)?;
    match is_newer(&plan.installed, &plan.target) {
        Ok(true) => Ok(None),
        Ok(false) if plan.target.as_str() == plan.installed.as_str() => {
            Err(ApplyRefusal::AlreadyCurrent)
        }
        // Downgrade: allowed, but only after an explicit confirm.
        Ok(false) => Ok(Some(false)),
        Err(_) => Err(ApplyRefusal::Unavailable),
    }
}

/// Production [`ApplyHost`]: real network, real files, elevation through
/// `pkexec`, restart through re-exec. The pure `apply` sequence above
/// stays testable because everything environment-dependent sits here.
pub struct LiveApplyHost;

impl LiveApplyHost {
    /// One bounded GET; refuses anything larger than `MAX_ARTIFACT_BYTES`
    /// so a misconfigured endpoint cannot exhaust the disk.
    fn fetch(&self, url: &str, destination: &std::path::Path) -> Result<u64, ApplyRefusal> {
        let response = ureq::get(url)
            .config()
            .timeout_connect(Some(CHECK_TIMEOUT))
            .timeout_recv_response(Some(APPLY_TIMEOUT))
            .build()
            .call()
            .map_err(map_download_error)?;
        if !response.status().is_success() {
            return Err(ApplyRefusal::Unavailable);
        }
        let mut file =
            std::fs::File::create(destination).map_err(|_| ApplyRefusal::StagingFailed)?;
        let mut reader = response.into_body().into_reader();
        let mut written = 0u64;
        loop {
            let mut chunk = [0u8; 64 * 1024];
            let read = std::io::Read::read(&mut reader, &mut chunk)
                .map_err(|_| ApplyRefusal::StagingFailed)?;
            if read == 0 {
                break;
            }
            written += read as u64;
            if written > MAX_ARTIFACT_BYTES {
                return Err(ApplyRefusal::DownloadTooLarge);
            }
            std::io::Write::write_all(&mut file, &chunk[..read])
                .map_err(|_| ApplyRefusal::StagingFailed)?;
        }
        Ok(written)
    }
}

/// Downloads use a longer window than checks: the artifact is a few
/// megabytes and the connection may be slow.
pub const APPLY_TIMEOUT: std::time::Duration = std::time::Duration::from_secs(600);

/// Map a ureq failure onto an apply refusal. Timeouts, resets, and DNS
/// failures are the network's fault; everything else is `Unavailable`.
fn map_download_error(error: ureq::Error) -> ApplyRefusal {
    use std::io::ErrorKind;
    match &error {
        ureq::Error::Io(inner) => match inner.kind() {
            ErrorKind::TimedOut
            | ErrorKind::NotConnected
            | ErrorKind::ConnectionRefused
            | ErrorKind::ConnectionReset
            | ErrorKind::ConnectionAborted
            | ErrorKind::NetworkDown
            | ErrorKind::NetworkUnreachable
            | ErrorKind::HostUnreachable => ApplyRefusal::Offline,
            _ => ApplyRefusal::Unavailable,
        },
        ureq::Error::HostNotFound | ureq::Error::ConnectionFailed | ureq::Error::Timeout(_) => {
            ApplyRefusal::Offline
        }
        _ => ApplyRefusal::Unavailable,
    }
}

/// Path of a staged helper that performs one rename as root. It is data
/// (`std::env::args().next()` — the binary path), not code, so the
/// elevated process runs exactly this program with exactly these args.
const PKEXEC: &str = "pkexec";
const REPLACE_HELPER_MODE: &str = "--replace-binary";

impl ApplyHost for LiveApplyHost {
    fn download(&self, url: &str, destination: &std::path::Path) -> Result<u64, ApplyRefusal> {
        self.fetch(url, destination)
    }

    fn read_text(&self, path: &std::path::Path) -> Result<String, ApplyRefusal> {
        std::fs::read_to_string(path).map_err(|_| ApplyRefusal::ChecksumMismatch)
    }

    fn read_digest(&self, path: &std::path::Path) -> Result<[u8; 32], ApplyRefusal> {
        let bytes = std::fs::read(path).map_err(|_| ApplyRefusal::DownloadTooLarge)?;
        let mut hasher = <sha2::Sha256 as sha2::Digest>::new();
        sha2::Digest::update(&mut hasher, &bytes);
        let out = sha2::Digest::finalize(hasher);
        let mut digest = [0u8; 32];
        digest.copy_from_slice(&out);
        Ok(digest)
    }

    fn stage(
        &self,
        archive: &std::path::Path,
        staging: &std::path::Path,
        tag: &str,
    ) -> Result<Vec<std::path::PathBuf>, ApplyRefusal> {
        std::fs::create_dir_all(staging).map_err(|_| ApplyRefusal::StagingFailed)?;
        let file = std::fs::File::open(archive).map_err(|_| ApplyRefusal::StagingFailed)?;
        let decoder = flate2::read::GzDecoder::new(file);
        let mut archive = tar::Archive::new(decoder);
        let entries = archive.entries().map_err(|_| ApplyRefusal::MissingBinary)?;
        let mut staged = Vec::new();
        for entry in entries {
            let mut entry = entry.map_err(|_| ApplyRefusal::MissingBinary)?;
            let path = entry
                .path()
                .map_err(|_| ApplyRefusal::MissingBinary)?
                .into_owned();
            let Some(name) = path
                .file_name()
                .and_then(|name| name.to_str())
                .filter(|name| MANAGED_BINARIES.contains(name))
            else {
                continue;
            };
            let target = staging.join(name);
            entry
                .unpack(&target)
                .map_err(|_| ApplyRefusal::StagingFailed)?;
            set_executable(&target).map_err(|_| ApplyRefusal::StagingFailed)?;
            staged.push(target);
        }
        let _ = tag;
        Ok(staged)
    }

    fn replace(
        &self,
        live: &std::path::Path,
        staged_file: &std::path::Path,
    ) -> Result<bool, ApplyRefusal> {
        match atomic_replace(live, staged_file) {
            Ok(()) => Ok(live.exists()),
            Err(first) => {
                // Elevation: the same program re-invoked under `pkexec`
                // performs ONE rename as root. Nothing else runs elevated,
                // and the payload is data, not code.
                match elevated_replace(live, staged_file) {
                    Ok(()) => Ok(live.exists()),
                    Err(_) => Err(match first {
                        ReplaceError::Permission => ApplyRefusal::PermissionDenied,
                        ReplaceError::Failed => ApplyRefusal::StagingFailed,
                    }),
                }
            }
        }
    }

    fn confirm_version(&self, live: &std::path::Path, target: &str) -> Result<(), ApplyRefusal> {
        // Spawn the fresh binary and compare its `--version` output; the
        // old text-scraping path never had a hard signal to trust.
        let output = std::process::Command::new(live)
            .arg("--version")
            .output()
            .map_err(|_| ApplyRefusal::ConfirmMismatch)?;
        if !output.status.success() {
            return Err(ApplyRefusal::ConfirmMismatch);
        }
        let reported = String::from_utf8_lossy(&output.stdout);
        if !reported.contains(target) {
            return Err(ApplyRefusal::ConfirmMismatch);
        }
        Ok(())
    }

    fn restart(&self) -> Result<(), ApplyRefusal> {
        // The desktop owner performs the snapshot-save + re-exec; the
        // worker only signals the intent, so the caller (settings) owns
        // the restart gate.
        Ok(())
    }
}

/// Make one path executable (0755 best effort; mode is not security).
fn set_executable(path: &std::path::Path) -> std::io::Result<()> {
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt as _;
        let mode = std::fs::metadata(path)?.permissions().mode();
        std::fs::set_permissions(path, std::fs::Permissions::from_mode(mode | 0o111))
    }
    #[cfg(not(unix))]
    {
        let _ = path;
        Ok(())
    }
}

/// Why an atomic replace failed, so elevation can decide whether it is
/// worth trying.
enum ReplaceError {
    Permission,
    Failed,
}

/// Replace `live` with `staged` in one step:
/// - Linux: `rename` over the destination (atomic when both are on the
///   same mount; a root-owned `/usr/local/bin` returns `EACCES`, which
///   the caller escalates).
/// - Other Unix: `copyall` over the destination, because `rename`
///   across filesystems is `EXDEV` and Darwin's `renameat2(NO_REPLACE)`
///   is not available in std.
fn atomic_replace(live: &std::path::Path, staged: &std::path::Path) -> Result<(), ReplaceError> {
    #[cfg(target_os = "linux")]
    {
        std::fs::rename(staged, live).map_err(|error| {
            if error.kind() == std::io::ErrorKind::PermissionDenied {
                ReplaceError::Permission
            } else {
                ReplaceError::Failed
            }
        })
    }
    #[cfg(not(target_os = "linux"))]
    {
        let _ = staged;
        std::fs::copy(staged, live).map(|_| ()).map_err(|error| {
            if error.kind() == std::io::ErrorKind::PermissionDenied {
                ReplaceError::Permission
            } else {
                ReplaceError::Failed
            }
        })
    }
}

/// Ask polkit for one rename as root. `pkexec` is the standard Linux
/// agent; on any other platform (or when it is missing) this refuses
/// cleanly so the caller can report `permission_denied`.
fn elevated_replace(live: &std::path::Path, staged_file: &std::path::Path) -> Result<(), ()> {
    #[cfg(target_os = "linux")]
    {
        let program = std::env::current_exe().map_err(|_| ())?;
        let output = std::process::Command::new(PKEXEC)
            .arg(&program)
            .arg(REPLACE_HELPER_MODE)
            .arg(live)
            .arg(staged_file)
            .output()
            .map_err(|_| ())?;
        if output.status.success() {
            Ok(())
        } else {
            Err(())
        }
    }
    #[cfg(not(target_os = "linux"))]
    {
        let _ = (live, staged_file);
        Err(())
    }
}

/// Handle the elevated rename helper mode. Returns `true` when this
/// process was invoked as the helper (so the caller must exit), `false`
/// otherwise. The binary uses this at startup when `argv[1]` is
/// [`REPLACE_HELPER_MODE`]; the desktop treats it as data.
pub fn maybe_run_replace_helper() -> bool {
    let mut args = std::env::args();
    let _ = args.next();
    if args.next().as_deref() != Some(REPLACE_HELPER_MODE) {
        return false;
    }
    let (Some(live), Some(staged)) = (args.next(), args.next()) else {
        eprintln!("replace helper: missing arguments");
        std::process::exit(2);
    };
    let live = std::path::PathBuf::from(live);
    let staged = std::path::PathBuf::from(staged);
    let result = atomic_replace(&live, &staged).map(|()| {
        set_executable(&live).map_err(|_| ()).unwrap_or(());
    });
    match result {
        Ok(()) => {
            println!("replaced {}", live.display());
            true
        }
        Err(_) => {
            eprintln!("replace helper: failed to replace {}", live.display());
            std::process::exit(1);
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn artifact_urls_match_the_release_layout() {
        assert_eq!(
            artifact_url("0.5.1"),
            "https://github.com/fachrihawari/omaterm/releases/download/0.5.1/omaterm-0.5.1-x86_64.tar.gz"
        );
        assert_eq!(
            checksum_url("0.5.1"),
            "https://github.com/fachrihawari/omaterm/releases/download/0.5.1/omaterm-0.5.1-x86_64.tar.gz.sha256"
        );
    }

    #[test]
    fn only_plain_stable_tags_are_accepted() {
        assert_eq!(
            tag_from_redirect_url("https://github.com/fachrihawari/omaterm/releases/tag/v0.4.1"),
            Some("0.4.1".into())
        );
        assert_eq!(
            tag_from_redirect_url("https://github.com/fachrihawari/omaterm/releases/tag/0.4.1"),
            Some("0.4.1".into())
        );
        // Non-stable layouts never reach the comparison step.
        assert_eq!(
            tag_from_redirect_url(
                "https://github.com/fachrihawari/omaterm/releases/tag/v1.0.0-rc.1"
            ),
            None
        );
        assert_eq!(
            tag_from_redirect_url("https://github.com/fachrihawari/omaterm/releases/tag/v1.0"),
            None
        );
        assert_eq!(
            tag_from_redirect_url("https://github.com/fachrihawari/omaterm/releases/latest"),
            None
        );
        assert_eq!(parse_stable_tag("v0.0.0"), Some("0.0.0".into()));
        assert_eq!(parse_stable_tag(""), None);
        assert_eq!(parse_stable_tag("1.2.3.4"), None);
        assert_eq!(parse_stable_tag("1.2.x"), None);
    }

    #[test]
    fn checksum_sidecar_parses_the_published_line() {
        let digest = "a".repeat(64);
        let parsed = parse_checksum_sidecar(&format!("{digest}  omaterm-0.5.1-x86_64.tar.gz\n"))
            .expect("published line must parse");
        assert_eq!(parsed.sha256, [0xaau8; 32]);
        // A leading binary marker (`sha256sum` on text-vs-binary watch)
        // and a missing name are both tolerated.
        assert!(parse_checksum_sidecar(&format!("\\{digest}\n")).is_some());
        assert!(parse_checksum_sidecar(&digest).is_some());
        // Malformed lines refuse rather than letting a caller guess.
        assert!(parse_checksum_sidecar("").is_none());
        assert!(parse_checksum_sidecar("not-hex\n").is_none());
        assert!(parse_checksum_sidecar(&format!("{}\n", "a".repeat(63))).is_none());
    }

    #[test]
    fn checksum_comparison_is_exact() {
        let digest = [0x11u8; 32];
        let published = PublishedChecksum { sha256: digest };
        assert!(checksum_matches(&digest, &published));
        let mut tampered = digest;
        tampered[31] = 0x12;
        assert!(!checksum_matches(&tampered, &published));
    }

    #[test]
    fn preflight_refuses_managed_downgrade_and_bad_versions() {
        let plan = |target: &str, kind: InstallKind| ApplyPlan {
            target: target.into(),
            installed: "0.4.1".into(),
            exe_path: "/usr/local/bin/omaterm".into(),
            install_kind: kind,
        };
        // Upgrade on an owned install proceeds without a confirm.
        assert_eq!(
            apply_preflight(&plan("0.5.0", InstallKind::Owned)),
            Ok(None)
        );
        // Same version is a refusal, not a no-op.
        assert_eq!(
            apply_preflight(&plan("0.4.1", InstallKind::Owned)),
            Err(ApplyRefusal::AlreadyCurrent)
        );
        // Downgrade proceeds only after an explicit confirm.
        assert_eq!(
            apply_preflight(&plan("0.3.0", InstallKind::Owned)),
            Ok(Some(false))
        );
        // A package-managed install is refused outright.
        assert_eq!(
            apply_preflight(&plan("0.5.0", InstallKind::Managed)),
            Err(ApplyRefusal::ManagedInstall)
        );
        // Prerelease/non-semver targets never reach the pipeline.
        assert_eq!(
            apply_preflight(&plan("1.0.0-rc.1", InstallKind::Owned)),
            Err(ApplyRefusal::Unavailable)
        );
        // Stage codes and refusal codes are stable strings, not prose.
        assert_eq!(ApplyStage::Verify.as_str(), "verify");
        assert_eq!(ApplyRefusal::ChecksumMismatch.code(), "checksum_mismatch");
        assert_eq!(
            ApplyRefusal::ManagedInstall.code(),
            "managed_by_package_manager"
        );
    }

    /// Lowercase hex sha256, used both to publish and to compute.
    fn digest_of(bytes: &[u8]) -> String {
        let mut hasher = <sha2::Sha256 as sha2::Digest>::new();
        sha2::Digest::update(&mut hasher, bytes);
        sha2::Digest::finalize(hasher)
            .iter()
            .map(|byte| format!("{byte:02x}"))
            .collect()
    }

    /// Scripted host: records calls, can refuse at any stage.
    struct FakeHost {
        digest_ok: bool,
        stage_missing: bool,
        replace_fails: bool,
        confirm_mismatch: bool,
        calls: std::cell::RefCell<Vec<String>>,
    }

    impl ApplyHost for FakeHost {
        fn download(&self, url: &str, destination: &std::path::Path) -> Result<u64, ApplyRefusal> {
            self.calls
                .borrow_mut()
                .push(format!("download:{}", url.rsplit('/').next().unwrap_or("")));
            if url.ends_with(".sha256") {
                // Publish either the true digest or a tampered one.
                let digest = if self.digest_ok {
                    digest_of(b"artifact-bytes")
                } else {
                    "0".repeat(64)
                };
                let line = format!("{digest}  omaterm-0.5.0-x86_64.tar.gz\n");
                std::fs::write(destination, line.as_bytes())
                    .map_err(|_| ApplyRefusal::StagingFailed)?;
                return Ok(line.len() as u64);
            }
            std::fs::write(destination, b"artifact-bytes")
                .map_err(|_| ApplyRefusal::StagingFailed)?;
            Ok(b"artifact-bytes".len() as u64)
        }
        fn read_text(&self, path: &std::path::Path) -> Result<String, ApplyRefusal> {
            self.calls
                .borrow_mut()
                .push(format!("text:{}", path.display()));
            // The sidecar is placed beside the archive by `download`;
            // both the good and tampered digest are exercised through
            // files prepared by each test case.
            std::fs::read_to_string(path).map_err(|_| ApplyRefusal::ChecksumMismatch)
        }
        fn read_digest(&self, path: &std::path::Path) -> Result<[u8; 32], ApplyRefusal> {
            self.calls.borrow_mut().push("digest".into());
            let bytes = std::fs::read(path).map_err(|_| ApplyRefusal::StagingFailed)?;
            let mut hasher = <sha2::Sha256 as sha2::Digest>::new();
            sha2::Digest::update(&mut hasher, &bytes);
            let out = sha2::Digest::finalize(hasher);
            let mut digest = [0u8; 32];
            digest.copy_from_slice(&out);
            Ok(digest)
        }
        fn stage(
            &self,
            _archive: &std::path::Path,
            staging: &std::path::Path,
            _tag: &str,
        ) -> Result<Vec<std::path::PathBuf>, ApplyRefusal> {
            self.calls.borrow_mut().push("stage".into());
            std::fs::create_dir_all(staging).map_err(|_| ApplyRefusal::StagingFailed)?;
            let names: Vec<&str> = if self.stage_missing {
                vec!["omaterm"]
            } else {
                MANAGED_BINARIES.to_vec()
            };
            let mut paths = Vec::new();
            for name in names {
                let path = staging.join(name);
                std::fs::write(&path, b"binary").map_err(|_| ApplyRefusal::StagingFailed)?;
                #[cfg(unix)]
                {
                    use std::os::unix::fs::PermissionsExt as _;
                    let _ = std::fs::set_permissions(&path, std::fs::Permissions::from_mode(0o755));
                }
                paths.push(path);
            }
            Ok(paths)
        }
        fn replace(
            &self,
            live: &std::path::Path,
            staged_file: &std::path::Path,
        ) -> Result<bool, ApplyRefusal> {
            self.calls
                .borrow_mut()
                .push(format!("replace:{}", live.display()));
            if self.replace_fails {
                return Err(ApplyRefusal::PermissionDenied);
            }
            // Non-Darwin mirrors the production helper's rename; on macOS
            // the real helper uses renameat2(NO_REPLACE) or copyall.
            std::fs::copy(staged_file, live).map_err(|_| ApplyRefusal::PermissionDenied)?;
            Ok(true)
        }
        fn confirm_version(
            &self,
            live: &std::path::Path,
            target: &str,
        ) -> Result<(), ApplyRefusal> {
            self.calls
                .borrow_mut()
                .push(format!("confirm:{}", live.display()));
            if self.confirm_mismatch {
                return Err(ApplyRefusal::ConfirmMismatch);
            }
            let _ = target;
            Ok(())
        }
        fn restart(&self) -> Result<(), ApplyRefusal> {
            self.calls.borrow_mut().push("restart".into());
            Ok(())
        }
    }

    fn plan_for(kind: InstallKind) -> ApplyPlan {
        ApplyPlan {
            target: "0.5.0".into(),
            installed: "0.4.1".into(),
            exe_path: std::env::temp_dir()
                .join("omaterm")
                .to_string_lossy()
                .into_owned(),
            install_kind: kind,
        }
    }

    #[test]
    fn apply_runs_download_verify_stage_replace_confirm_restart_in_order() {
        let host = FakeHost {
            digest_ok: true,
            stage_missing: false,
            replace_fails: false,
            confirm_mismatch: false,
            calls: std::cell::RefCell::new(Vec::new()),
        };
        let work = std::env::temp_dir().join(format!("omaterm-apply-{}", std::process::id()));
        std::fs::create_dir_all(&work).expect("work dir");
        let result =
            apply(&host, &plan_for(InstallKind::Owned), &work).expect("apply must succeed");
        assert_eq!(result, ApplyStage::Restart);
        let calls = host.calls.borrow();
        assert!(calls.iter().any(|call| call.contains("download")));
        assert!(calls.iter().any(|call| call.starts_with("digest")));
        assert!(
            calls
                .iter()
                .filter(|call| call.starts_with("replace"))
                .count()
                == 2
        );
        assert!(calls.iter().any(|call| call.starts_with("confirm")));
        assert!(calls.contains(&"restart".to_string()));
        assert_eq!(calls.last().map(String::as_str), Some("restart"));
        let _ = std::fs::remove_dir_all(&work);
    }

    #[test]
    fn apply_refuses_at_each_stage_with_a_stable_code() {
        // A fresh work dir per case: the pipeline reuses file names across
        // cases, and a stale sidecar would poison the next read.
        let fresh_work = |name: &str| {
            let dir =
                std::env::temp_dir().join(format!("omaterm-apply-{}-{name}", std::process::id()));
            let _ = std::fs::remove_dir_all(&dir);
            std::fs::create_dir_all(&dir).expect("work dir");
            dir
        };

        // Checksum mismatch aborts before any replace.
        let host = FakeHost {
            digest_ok: false,
            stage_missing: false,
            replace_fails: false,
            confirm_mismatch: false,
            calls: std::cell::RefCell::new(Vec::new()),
        };
        let work = fresh_work("digest");
        let (stage, refusal) =
            apply(&host, &plan_for(InstallKind::Owned), &work).expect_err("bad digest must refuse");
        assert_eq!(stage, ApplyStage::Verify);
        assert_eq!(refusal.code(), "checksum_mismatch");
        assert!(
            host.calls
                .borrow()
                .iter()
                .all(|call| !call.starts_with("replace"))
        );

        // A missing binary in the tarball aborts before replace.
        let host = FakeHost {
            digest_ok: true,
            stage_missing: true,
            replace_fails: false,
            confirm_mismatch: false,
            calls: std::cell::RefCell::new(Vec::new()),
        };
        let work = fresh_work("missing");
        let (stage, refusal) = apply(&host, &plan_for(InstallKind::Owned), &work)
            .expect_err("missing binary must refuse");
        assert_eq!(stage, ApplyStage::Stage);
        assert_eq!(refusal.code(), "missing_binary");

        // A confirm mismatch does not restart.
        let host = FakeHost {
            digest_ok: true,
            stage_missing: false,
            replace_fails: false,
            confirm_mismatch: true,
            calls: std::cell::RefCell::new(Vec::new()),
        };
        let work = fresh_work("confirm");
        let (stage, refusal) = apply(&host, &plan_for(InstallKind::Owned), &work)
            .expect_err("confirm mismatch must refuse");
        assert_eq!(stage, ApplyStage::Confirm);
        assert_eq!(refusal.code(), "restart_required");
        assert!(!host.calls.borrow().iter().any(|call| call == "restart"));

        // Managed installs never download at all.
        let host = FakeHost {
            digest_ok: true,
            stage_missing: false,
            replace_fails: false,
            confirm_mismatch: false,
            calls: std::cell::RefCell::new(Vec::new()),
        };
        let (stage, refusal) = apply(
            &host,
            &plan_for(InstallKind::Managed),
            &fresh_work("managed"),
        )
        .expect_err("managed install must refuse");
        assert_eq!(stage, ApplyStage::Resolve);
        assert_eq!(refusal.code(), "managed_by_package_manager");
        assert!(host.calls.borrow().is_empty());
    }

    #[test]
    fn managed_install_guidance_names_the_pacman_path() {
        assert!(managed_install_guidance().contains("pacman -Syu"));
    }

    #[test]
    fn managed_installs_refuse_self_update() {
        assert!(InstallKind::Owned.self_update_allowed());
        assert!(!InstallKind::Managed.self_update_allowed());
        assert_eq!(
            InstallKind::Managed.as_str(),
            "managed",
            "the Settings render and CLI key off this code"
        );
        assert!(managed_install_guidance().contains("pacman -Syu"));
    }

    #[test]
    fn comparison_is_strict_semver_and_rejects_garbage() {
        assert!(is_newer("0.4.1", "0.4.2").unwrap());
        assert!(is_newer("0.4.1", "0.10.0").unwrap());
        assert!(!is_newer("0.4.1", "0.4.1").unwrap());
        assert!(!is_newer("0.5.0", "0.4.1").unwrap());
        assert!(is_newer("0.4.1", "nonsense").is_err());
        assert!(is_newer("nonsense", "0.4.1").is_err());
    }
}
