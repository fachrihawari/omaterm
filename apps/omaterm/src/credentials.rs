use std::collections::HashMap;
use std::fs::{self, OpenOptions};
use std::io::Write;
use std::path::{Path, PathBuf};
use std::time::{Duration, Instant};

use omaterm_core::{CommandContext, ProjectId, SessionId};
use omaterm_protocol::CapabilityToken;

/// Owner-thread credential state. Three independently random UUID v4 values
/// provide more than 256 bits of unpredictable material after version bits.
fn file_identity(path: &Path) -> std::io::Result<(u64, u64)> {
    #[cfg(unix)]
    {
        use std::os::unix::fs::MetadataExt;
        let metadata = fs::symlink_metadata(path)?;
        Ok((metadata.dev(), metadata.ino()))
    }
    #[cfg(windows)]
    {
        // `MetadataExt::file_index` is unstable. The volume serial plus the
        // 64-bit file index identifies this file, not a path or an mtime.
        windows_file_id(path)
    }
}

#[cfg(windows)]
fn windows_file_id(path: &Path) -> std::io::Result<(u64, u64)> {
    use std::os::windows::ffi::OsStrExt;
    use windows_sys::Win32::Foundation::{CloseHandle, INVALID_HANDLE_VALUE};
    use windows_sys::Win32::Storage::FileSystem::{
        BY_HANDLE_FILE_INFORMATION, CreateFileW, FILE_FLAG_BACKUP_SEMANTICS,
        FILE_FLAG_OPEN_REPARSE_POINT, FILE_READ_ATTRIBUTES, FILE_SHARE_DELETE, FILE_SHARE_READ,
        FILE_SHARE_WRITE, GetFileInformationByHandle, OPEN_EXISTING,
    };

    let wide: Vec<u16> = path
        .as_os_str()
        .encode_wide()
        .chain(std::iter::once(0))
        .collect();
    // SAFETY: `wide` is NUL-terminated. OPEN_REPARSE_POINT identifies the
    // object itself, so a swapped symlink does not report the target's id.
    let handle = unsafe {
        CreateFileW(
            wide.as_ptr(),
            FILE_READ_ATTRIBUTES,
            FILE_SHARE_READ | FILE_SHARE_WRITE | FILE_SHARE_DELETE,
            std::ptr::null(),
            OPEN_EXISTING,
            FILE_FLAG_BACKUP_SEMANTICS | FILE_FLAG_OPEN_REPARSE_POINT,
            std::ptr::null_mut(),
        )
    };
    if handle == INVALID_HANDLE_VALUE {
        return Err(std::io::Error::last_os_error());
    }
    let mut info = unsafe { std::mem::zeroed::<BY_HANDLE_FILE_INFORMATION>() };
    // SAFETY: `handle` is open and `info` is a live out-param.
    let ok = unsafe { GetFileInformationByHandle(handle, &mut info) };
    unsafe { CloseHandle(handle) };
    if ok == 0 {
        return Err(std::io::Error::last_os_error());
    }
    let index = (u64::from(info.nFileIndexHigh) << 32) | u64::from(info.nFileIndexLow);
    Ok((u64::from(info.dwVolumeSerialNumber), index))
}

fn secret() -> String {
    (0..3)
        .map(|_| SessionId::new().0.simple().to_string())
        .collect()
}

pub struct Credentials {
    local: String,
    file: PathBuf,
    file_identity: (u64, u64),
    scoped: HashMap<String, (SessionId, ProjectId, Instant)>,
    sessions: HashMap<SessionId, String>,
}

impl Credentials {
    pub fn create(socket: &Path) -> std::io::Result<Self> {
        let local = secret();
        let file = socket.with_extension("credential");
        let temporary =
            socket.with_extension(format!("credential-{}", SessionId::new().0.simple()));
        let result = (|| {
            let mut options = OpenOptions::new();
            options.write(true).create_new(true);
            #[cfg(unix)]
            {
                use std::os::unix::fs::OpenOptionsExt;
                options.mode(0o600);
            }
            let mut writer = options.open(&temporary)?;
            writer.write_all(local.as_bytes())?;
            writer.sync_all()?;
            fs::rename(&temporary, &file)?;
            #[cfg(windows)]
            omaterm_ipc::restrict_to_current_user(&file)?;
            file_identity(&file)
        })();
        let _ = fs::remove_file(temporary);
        Ok(Self {
            local,
            file,
            file_identity: result?,
            scoped: HashMap::new(),
            sessions: HashMap::new(),
        })
    }

    pub fn new_session_token(&self) -> String {
        secret()
    }

    pub fn publish(&mut self, token: String, session: SessionId, project: ProjectId) {
        self.publish_with_ttl(token, session, project, Duration::from_secs(24 * 60 * 60));
    }

    fn publish_with_ttl(
        &mut self,
        token: String,
        session: SessionId,
        project: ProjectId,
        ttl: Duration,
    ) {
        self.scoped
            .insert(token.clone(), (session, project, Instant::now() + ttl));
        self.sessions.insert(session, token);
    }

    pub fn revoke(&mut self, session: SessionId) {
        if let Some(token) = self.sessions.remove(&session) {
            self.scoped.remove(&token);
        }
    }

    pub fn authenticate(&self, token: Option<&CapabilityToken>) -> Option<CommandContext> {
        let token = token?.expose_for_transport();
        if token == self.local {
            return Some(CommandContext::LocalUser);
        }
        self.scoped.get(token).and_then(|(_, project, expiry)| {
            (Instant::now() < *expiry).then_some(CommandContext::Project(*project))
        })
    }
}

impl Drop for Credentials {
    fn drop(&mut self) {
        if file_identity(&self.file).ok() == Some(self.file_identity) {
            let _ = fs::remove_file(&self.file);
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn local_and_session_credentials_have_distinct_scopes_and_revoke() {
        let directory = std::env::temp_dir().join(format!("omaterm-cred-{}", SessionId::new().0));
        fs::create_dir(&directory).unwrap();
        let socket = directory.join("omaterm.sock");
        let mut credentials = Credentials::create(&socket).unwrap();
        let metadata = fs::metadata(socket.with_extension("credential")).unwrap();
        #[cfg(unix)]
        {
            use std::os::unix::fs::MetadataExt;
            assert_eq!(metadata.mode() & 0o777, 0o600);
        }
        #[cfg(windows)]
        {
            let _ = metadata;
        }
        let local = CapabilityToken::from_secret(credentials.local.clone()).unwrap();
        assert_eq!(
            credentials.authenticate(Some(&local)),
            Some(CommandContext::LocalUser)
        );
        let scoped = credentials.new_session_token();
        let project = ProjectId::new();
        let session = SessionId::new();
        credentials.publish(scoped.clone(), session, project);
        let token = CapabilityToken::from_secret(scoped).unwrap();
        assert_eq!(
            credentials.authenticate(Some(&token)),
            Some(CommandContext::Project(project))
        );
        credentials.revoke(session);
        assert_eq!(credentials.authenticate(Some(&token)), None);
        let expired = credentials.new_session_token();
        credentials.publish_with_ttl(expired.clone(), SessionId::new(), project, Duration::ZERO);
        let expired = CapabilityToken::from_secret(expired).unwrap();
        assert_eq!(credentials.authenticate(Some(&expired)), None);
        drop(credentials);
        assert!(!socket.with_extension("credential").exists());
        fs::remove_dir(directory).unwrap();
    }
}
