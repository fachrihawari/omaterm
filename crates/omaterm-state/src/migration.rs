use crate::{SnapshotError, WorkspaceSnapshot};

pub(crate) fn decode(
    bytes: &[u8],
    limits: crate::SnapshotLimits,
) -> Result<WorkspaceSnapshot, SnapshotError> {
    if bytes.len() > limits.max_file_bytes {
        return Err(SnapshotError::TooLarge);
    }
    let value: serde_json::Value =
        serde_json::from_slice(bytes).map_err(|error| SnapshotError::Corrupt(error.to_string()))?;
    let version = value
        .get("schema_version")
        .and_then(serde_json::Value::as_u64)
        .and_then(|value| u32::try_from(value).ok())
        .ok_or_else(|| SnapshotError::Corrupt("missing or invalid schema_version".into()))?;
    if version != WorkspaceSnapshot::SCHEMA_VERSION
        && version != WorkspaceSnapshot::V1_SCHEMA_VERSION
    {
        return Err(SnapshotError::UnsupportedVersion(version));
    }
    let snapshot: WorkspaceSnapshot =
        serde_json::from_value(value).map_err(|error| SnapshotError::Corrupt(error.to_string()))?;
    snapshot.validate(limits)?;
    Ok(snapshot)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn classifies_future_versions_before_decoding_their_body() {
        let error = decode(
            br#"{"schema_version":77,"future_shape":true}"#,
            crate::SnapshotLimits::default(),
        )
        .unwrap_err();
        assert!(matches!(error, SnapshotError::UnsupportedVersion(77)));
    }
}
