// Voice input for the compose box: records the microphone, makes 16 kHz
// mono 16-bit WAV of it here in the page, and sends that to the computer,
// whose whisper-server writes it down.
//
//   import { voiceStatus, canRecord, record, transcribe } from "./voice.js";
//   const st = await voiceStatus();        // {enabled, ready, reason, language, languages}
//   const rec = await record({ onLimit }); // throws when the mic is refused
//   const wav = await rec.stop();          // or rec.cancel()
//   const text = await transcribe(wav, "id");
//
// MediaRecorder isn't used: its formats (WebM on Chrome, MP4 on iOS) would
// need ffmpeg on the computer, and whisper takes WAV as it is.

const RATE = 16000;
// MAX_SECONDS is as long as the computer takes a recording.
export const MAX_SECONDS = 120;

// voiceStatus is voice input as the computer tells it; off when it can't
// tell.
export async function voiceStatus() {
  try {
    const res = await fetch("/api/voice");
    if (!res.ok) return { enabled: false };
    return await res.json();
  } catch {
    return { enabled: false };
  }
}

// canRecord tells why the page can't record, or "" when it can. Browsers
// give the microphone to HTTPS pages only (and localhost).
export function canRecord() {
  if (!window.isSecureContext) return "The microphone needs HTTPS: open web access through Tailscale or Cloudflare.";
  if (!navigator.mediaDevices?.getUserMedia) return "This browser can't record.";
  return "";
}

// record starts the microphone. onLimit is called as the recording reaches
// MAX_SECONDS, after which it records no more.
export async function record({ onLimit } = {}) {
  // Made before the first await, while the tap still counts as one: iOS
  // keeps an audio context made later silent.
  const AC = window.AudioContext || window.webkitAudioContext;
  const ctx = new AC();
  let stream;
  try {
    stream = await navigator.mediaDevices.getUserMedia({
      audio: { channelCount: 1, echoCancellation: true, noiseSuppression: true, autoGainControl: true },
    });
  } catch (err) {
    ctx.close();
    throw err;
  }
  await ctx.resume();
  const source = ctx.createMediaStreamSource(stream);
  // ScriptProcessor is old, but every phone browser has it, and it needs no
  // module of its own as an AudioWorklet would.
  const node = ctx.createScriptProcessor(4096, 1, 1);
  const chunks = [];
  let length = 0;
  const limit = ctx.sampleRate * MAX_SECONDS;
  let full = false;
  node.onaudioprocess = (e) => {
    if (full) return;
    const data = e.inputBuffer.getChannelData(0);
    const take = Math.min(data.length, limit - length);
    chunks.push(data.slice(0, take));
    length += take;
    if (length >= limit) {
      full = true;
      onLimit?.();
    }
  };
  source.connect(node);
  // Connected to the output, or Chrome never runs it; it writes nothing
  // there, so nothing is heard.
  node.connect(ctx.destination);

  let ended = false;
  const end = () => {
    if (ended) return;
    ended = true;
    node.onaudioprocess = null;
    try { source.disconnect(); node.disconnect(); } catch {}
    for (const t of stream.getTracks()) t.stop();
  };
  return {
    // seconds is how long it has recorded.
    seconds: () => length / ctx.sampleRate,
    // stop ends the recording and gives it as a WAV.
    async stop() {
      end();
      const rate = ctx.sampleRate;
      ctx.close();
      const pcm = new Float32Array(length);
      let at = 0;
      for (const c of chunks) { pcm.set(c, at); at += c.length; }
      return encodeWAV(await resample(pcm, rate));
    },
    cancel() {
      end();
      ctx.close();
    },
  };
}

// resample brings samples at rate down to 16 kHz.
async function resample(pcm, rate) {
  if (rate === RATE || pcm.length === 0) return pcm;
  const frames = Math.max(1, Math.ceil((pcm.length * RATE) / rate));
  const off = new OfflineAudioContext(1, frames, RATE);
  const buf = off.createBuffer(1, pcm.length, rate);
  buf.copyToChannel(pcm, 0);
  const src = off.createBufferSource();
  src.buffer = buf;
  src.connect(off.destination);
  src.start();
  return (await off.startRendering()).getChannelData(0);
}

// encodeWAV is 16 kHz mono samples as a 16-bit PCM WAV.
function encodeWAV(samples) {
  const out = new DataView(new ArrayBuffer(44 + samples.length * 2));
  const str = (at, s) => { for (let i = 0; i < s.length; i++) out.setUint8(at + i, s.charCodeAt(i)); };
  str(0, "RIFF");
  out.setUint32(4, 36 + samples.length * 2, true);
  str(8, "WAVE");
  str(12, "fmt ");
  out.setUint32(16, 16, true);
  out.setUint16(20, 1, true); // PCM
  out.setUint16(22, 1, true); // mono
  out.setUint32(24, RATE, true);
  out.setUint32(28, RATE * 2, true);
  out.setUint16(32, 2, true);
  out.setUint16(34, 16, true);
  str(36, "data");
  out.setUint32(40, samples.length * 2, true);
  for (let i = 0; i < samples.length; i++) {
    const s = Math.max(-1, Math.min(1, samples[i]));
    out.setInt16(44 + i * 2, s < 0 ? s * 0x8000 : s * 0x7fff, true);
  }
  return new Blob([out.buffer], { type: "audio/wav" });
}

// transcribe sends the WAV to the computer and gives back its text, or
// throws with the computer's reason.
export async function transcribe(wav, lang) {
  const q = lang ? `?lang=${encodeURIComponent(lang)}` : "";
  const res = await fetch(`/api/voice${q}`, { method: "POST", headers: { "Content-Type": "audio/wav" }, body: wav });
  if (!res.ok) {
    const why = (await res.text().catch(() => "")).trim();
    throw new Error(why || `Not written down (${res.status})`);
  }
  return (await res.json()).text || "";
}
