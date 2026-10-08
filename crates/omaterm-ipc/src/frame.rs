//! Newline-delimited JSON framing shared by the Unix socket and the Windows
//! named pipe. The transports differ only in how a byte is read or written.

use std::io::{ErrorKind, Result};
use std::sync::atomic::{AtomicBool, Ordering};
use std::time::{Duration, Instant};

use omaterm_protocol::{
    IpcRequest, IpcResponse, MAX_REQUEST_FRAME, encode_response, validate_request,
};

pub(crate) trait FrameIo {
    fn read_with_timeout(&mut self, buf: &mut [u8], timeout: Duration) -> Result<usize>;
    fn write_all_with_timeout(&mut self, buf: &[u8], timeout: Duration) -> Result<()>;
}

/// Read one request, run `handler`, and write the response until the peer
/// closes or `stopping` is set. A frame or request deadline closes the
/// connection instead of leaving it half-open.
pub(crate) fn serve_requests(
    stream: &mut dyn FrameIo,
    handler: &dyn Fn(IpcRequest, Instant) -> IpcResponse,
    stopping: &AtomicBool,
    frame_limit: Duration,
    request_limit: Duration,
) -> Result<()> {
    loop {
        if stopping.load(Ordering::Acquire) {
            return Ok(());
        }
        let started = Instant::now();
        let frame_deadline = started + frame_limit;
        let request_deadline = started + request_limit;
        let mut frame = Vec::with_capacity(1024);
        let mut byte = [0u8; 1];
        loop {
            if stopping.load(Ordering::Acquire) {
                return Ok(());
            }
            let remaining = frame_deadline.saturating_duration_since(Instant::now());
            if remaining.is_zero() {
                return Ok(());
            }
            match stream.read_with_timeout(&mut byte, remaining.min(Duration::from_millis(200))) {
                Ok(0) => return Ok(()),
                Ok(_) if byte[0] == b'\n' => break,
                Ok(_) => {
                    if frame.len() >= MAX_REQUEST_FRAME - 1 {
                        write_failure(
                            stream,
                            "",
                            "invalid_request",
                            "request exceeds the 64 KiB frame limit",
                        )?;
                        return Ok(());
                    }
                    frame.push(byte[0]);
                }
                Err(error) if error.kind() == ErrorKind::Interrupted => continue,
                Err(error)
                    if matches!(error.kind(), ErrorKind::WouldBlock | ErrorKind::TimedOut) =>
                {
                    continue;
                }
                Err(error) => return Err(error),
            }
        }
        let request: IpcRequest = match serde_json::from_slice(&frame) {
            Ok(request) => request,
            Err(_) => {
                write_failure(stream, "", "invalid_request", "malformed JSON request")?;
                return Ok(());
            }
        };
        let request_id = request.request_id.clone();
        let response = match validate_request(&request) {
            Ok(()) => handler(request, request_deadline),
            Err(error) => IpcResponse::failure(request_id, error.code, error.message),
        };
        let remaining = request_deadline.saturating_duration_since(Instant::now());
        if remaining.is_zero() {
            write_response(
                stream,
                &IpcResponse::failure(response.request_id, "timeout", "request deadline exceeded"),
                Duration::from_secs(1),
            )?;
            return Ok(());
        }
        write_response(stream, &response, remaining)?;
    }
}

/// Read one newline-terminated payload. The caller owns the frame-size cap.
pub(crate) fn read_delimited_frame(
    stream: &mut dyn FrameIo,
    deadline: Instant,
    max_len: usize,
) -> Result<Vec<u8>> {
    let mut frame = Vec::with_capacity(1024);
    let mut byte = [0u8; 1];
    loop {
        let remaining = deadline.saturating_duration_since(Instant::now());
        if remaining.is_zero() {
            return Err(std::io::Error::new(
                ErrorKind::TimedOut,
                "IPC request deadline exceeded",
            ));
        }
        match stream.read_with_timeout(&mut byte, remaining) {
            Ok(0) => {
                return Err(std::io::Error::new(
                    ErrorKind::UnexpectedEof,
                    "IPC connection closed",
                ));
            }
            Ok(_) => {}
            Err(error) if error.kind() == ErrorKind::Interrupted => continue,
            Err(error) => return Err(error),
        }
        if byte[0] == b'\n' {
            break;
        }
        if frame.len() >= max_len.saturating_sub(1) {
            return Err(std::io::Error::new(
                ErrorKind::InvalidData,
                "response exceeds frame limit",
            ));
        }
        frame.push(byte[0]);
    }
    Ok(frame)
}

fn write_failure(
    stream: &mut dyn FrameIo,
    request_id: impl Into<String>,
    code: &str,
    message: &str,
) -> Result<()> {
    write_response(
        stream,
        &IpcResponse::failure(request_id.into(), code, message),
        Duration::from_secs(1),
    )
}

fn write_response(
    stream: &mut dyn FrameIo,
    response: &IpcResponse,
    timeout: Duration,
) -> Result<()> {
    match encode_response(response) {
        Ok(frame) => stream.write_all_with_timeout(&frame, timeout),
        Err(_) => {
            let frame = encode_response(&IpcResponse::failure(
                response.request_id.clone(),
                "response_too_large",
                "response exceeds the 1 MiB frame limit",
            ))
            .unwrap_or_else(|_| b"{}\n".to_vec());
            stream.write_all_with_timeout(&frame, timeout)
        }
    }
}
