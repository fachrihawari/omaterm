//! Versioned, transport-independent JSON wire types for local OmaTerm IPC.

use serde::{Deserialize, Serialize};
pub mod method;

pub const PROTOCOL_VERSION: u32 = 1;
pub const MAX_REQUEST_FRAME: usize = 64 * 1024;
pub const MAX_RESPONSE_FRAME: usize = 1024 * 1024;
pub const MAX_CONNECTIONS: usize = 32;
pub const MAX_READ_LINES: usize = 1_000;
pub const MAX_READ_COLUMNS: usize = 1_000;
pub const MAX_SEND_BYTES: usize = 8 * 1024;
pub const MAX_ARG_COUNT: usize = 256;
pub const MAX_ARGUMENT_BYTES: usize = 4 * 1024;
pub const MAX_JOURNAL_ENTRIES: usize = 1_000;

#[derive(Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct IpcRequest {
    pub version: u32,
    pub request_id: String,
    pub method: String,
    #[serde(default)]
    pub params: serde_json::Value,
    #[serde(default)]
    pub token: Option<CapabilityToken>,
}

impl std::fmt::Debug for IpcRequest {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("IpcRequest")
            .field("version", &self.version)
            .field("request_id", &self.request_id)
            .field("method", &self.method)
            .field("params", &"[REDACTED]")
            .field("token", &self.token)
            .finish()
    }
}

#[derive(Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(transparent)]
pub struct CapabilityToken(String);

impl CapabilityToken {
    pub fn from_secret(secret: String) -> Option<Self> {
        (secret.len() >= 43
            && secret.len() <= 128
            && secret
                .bytes()
                .all(|b| b.is_ascii_alphanumeric() || b == b'_' || b == b'-'))
        .then_some(Self(secret))
    }

    pub fn expose_for_transport(&self) -> &str {
        &self.0
    }
}

impl std::fmt::Debug for CapabilityToken {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str("CapabilityToken([REDACTED])")
    }
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct IpcResponse {
    pub version: u32,
    pub request_id: String,
    pub ok: bool,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub result: Option<serde_json::Value>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub error: Option<IpcError>,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct IpcError {
    pub code: String,
    pub message: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub details: Option<String>,
}

impl IpcResponse {
    pub fn success(request_id: String, result: serde_json::Value) -> Self {
        Self {
            version: PROTOCOL_VERSION,
            request_id,
            ok: true,
            result: Some(result),
            error: None,
        }
    }

    pub fn failure(
        request_id: String,
        code: impl Into<String>,
        message: impl Into<String>,
    ) -> Self {
        Self {
            version: PROTOCOL_VERSION,
            request_id,
            ok: false,
            result: None,
            error: Some(IpcError {
                code: code.into(),
                message: message.into(),
                details: None,
            }),
        }
    }
}

/// Apply syntactic/protocol-level checks before method decoding or dispatch.
pub fn validate_request(request: &IpcRequest) -> Result<(), IpcError> {
    let invalid = |message: &str| IpcError {
        code: "invalid_request".into(),
        message: message.into(),
        details: None,
    };
    if request.request_id.is_empty()
        || request.request_id.len() > 128
        || request.request_id.chars().any(char::is_control)
    {
        return Err(invalid("request_id must be 1..=128 printable bytes"));
    }
    if request.method.is_empty()
        || request.method.len() > 64
        || request.method.chars().any(char::is_control)
    {
        return Err(invalid("method must be 1..=64 printable bytes"));
    }
    if request
        .token
        .as_ref()
        .is_some_and(|token| CapabilityToken::from_secret(token.0.clone()).is_none())
    {
        return Err(IpcError {
            code: "invalid_request".into(),
            message: "token has an invalid encoding or length".into(),
            details: None,
        });
    }
    if request.version != PROTOCOL_VERSION {
        return Err(IpcError {
            code: "unsupported_version".into(),
            message: format!("protocol version {} is not supported", request.version),
            details: None,
        });
    }
    Ok(())
}

/// Serialize and enforce the complete newline-inclusive response frame cap.
pub fn encode_response(response: &IpcResponse) -> Result<Vec<u8>, IpcError> {
    let mut bounded = response.clone();
    loop {
        let mut bytes = serde_json::to_vec(&bounded).map_err(|_| IpcError {
            code: "runtime_failure".into(),
            message: "response serialization failed".into(),
            details: None,
        })?;
        bytes.push(b'\n');
        if bytes.len() <= MAX_RESPONSE_FRAME {
            return Ok(bytes);
        }
        let Some(result) = bounded.result.as_mut() else {
            return Err(IpcError {
                code: "response_too_large".into(),
                message: "response exceeds the 1 MiB frame limit".into(),
                details: None,
            });
        };
        let Some((text, shortened)) = longest_string_mut(result) else {
            return Err(IpcError {
                code: "response_too_large".into(),
                message: "response exceeds the 1 MiB frame limit".into(),
                details: None,
            });
        };
        *text = shortened;
        if let Some(object) = result.as_object_mut() {
            object.insert("truncated".into(), serde_json::Value::Bool(true));
        }
    }
}

fn longest_string_mut(value: &mut serde_json::Value) -> Option<(&mut String, String)> {
    match value {
        serde_json::Value::String(text) if !text.is_empty() => {
            let count = text.chars().count();
            let shortened = text.chars().take(count / 2).collect();
            Some((text, shortened))
        }
        serde_json::Value::Array(items) => items
            .iter_mut()
            .filter_map(longest_string_mut)
            .max_by_key(|(text, _)| text.len()),
        serde_json::Value::Object(items) => items
            .values_mut()
            .filter_map(longest_string_mut)
            .max_by_key(|(text, _)| text.len()),
        _ => None,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn request_and_response_round_trip() {
        let request = IpcRequest {
            version: 1,
            request_id: "req-1".into(),
            method: "pane.list".into(),
            params: serde_json::json!({}),
            token: None,
        };
        let decoded: IpcRequest =
            serde_json::from_slice(&serde_json::to_vec(&request).unwrap()).unwrap();
        assert_eq!(request, decoded);
        let response = IpcResponse::success("req-1".into(), serde_json::json!({"panes": []}));
        let decoded: IpcResponse =
            serde_json::from_slice(&encode_response(&response).unwrap()).unwrap();
        assert_eq!(decoded, response);
    }

    #[test]
    fn rejects_unknown_fields_bad_ids_and_versions() {
        assert!(
            serde_json::from_str::<IpcRequest>(
                r#"{"version":1,"request_id":"x","method":"pane.list","extra":true}"#
            )
            .is_err()
        );
        let bad = IpcRequest {
            version: 2,
            request_id: "id".into(),
            method: "pane.list".into(),
            params: serde_json::json!({}),
            token: None,
        };
        assert_eq!(
            validate_request(&bad).unwrap_err().code,
            "unsupported_version"
        );
        let bad = IpcRequest {
            version: 1,
            request_id: "\n".into(),
            method: "pane.list".into(),
            params: serde_json::json!({}),
            token: None,
        };
        assert_eq!(validate_request(&bad).unwrap_err().code, "invalid_request");
    }

    #[test]
    fn capability_debug_is_redacted() {
        let token = CapabilityToken::from_secret("a".repeat(43)).unwrap();
        assert!(!format!("{token:?}").contains(&"a".repeat(20)));
        let request = IpcRequest {
            version: 1,
            request_id: "safe".into(),
            method: "terminal.send".into(),
            params: serde_json::json!({"data":"private-payload"}),
            token: Some(token),
        };
        let summary = format!("{request:?}");
        assert!(!summary.contains("private-payload"));
        assert!(!summary.contains(&"a".repeat(20)));
    }

    #[test]
    fn oversized_text_is_utf8_safely_truncated_and_marked() {
        let response = IpcResponse::success(
            "request".into(),
            serde_json::json!({"text": format!("{}é", "x".repeat(MAX_RESPONSE_FRAME))}),
        );
        let frame = encode_response(&response).unwrap();
        assert!(frame.len() <= MAX_RESPONSE_FRAME);
        let decoded: IpcResponse = serde_json::from_slice(&frame).unwrap();
        assert_eq!(decoded.result.unwrap()["truncated"], true);
    }

    #[test]
    fn rejects_malformed_capability_encoding() {
        let request = IpcRequest {
            version: 1,
            request_id: "request".into(),
            method: "pane.list".into(),
            params: serde_json::json!({}),
            token: Some(CapabilityToken("short".into())),
        };
        assert_eq!(
            validate_request(&request).unwrap_err().code,
            "invalid_request"
        );
    }
}
