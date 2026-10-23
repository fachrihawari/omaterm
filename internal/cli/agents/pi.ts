// Installed by omaterm (`omaterm skill setup`), which replaces it as omaterm
// updates; `omaterm skill remove` removes it. Inside omaterm, it tells Pi which
// session and pane it runs in, and tells omaterm Pi's session, to resume it
// after omaterm's background service restarts. Outside omaterm it does nothing.
import { execFile } from "node:child_process";

const OMATERM = __OMATERM__;

// hook runs `omaterm hook pi` as Claude Code runs its hooks, returning what
// omaterm tells the agent.
function hook(sessionID: string | undefined): Promise<string> {
  return new Promise((resolve) => {
    const child = execFile(OMATERM, ["hook", "pi"], { timeout: 5000 }, (err, stdout) => {
      try {
        resolve(JSON.parse(stdout).hookSpecificOutput.additionalContext ?? "");
      } catch {
        resolve("");
      }
    });
    child.on("error", () => resolve(""));
    child.stdin?.end(JSON.stringify({ hook_event_name: "SessionStart", session_id: sessionID ?? "" }));
  });
}

export default function (pi: any) {
  if (!process.env.OMATERM_PANE) return;
  let context = "";
  pi.on("session_start", async (_event: any, ctx: any) => {
    // A terminal session, or a one-off prompt; not one a program drives.
    if (ctx?.mode !== "tui" && ctx?.mode !== "print") return;
    let sessionID: string | undefined;
    try {
      if (ctx.mode === "tui" && ctx.sessionManager.getSessionFile()) {
        sessionID = ctx.sessionManager.getSessionId();
      }
    } catch {}
    context = await hook(sessionID);
  });
  pi.on("before_agent_start", (event: any) => {
    if (context) event.systemPromptOptions.sections.omaterm = context;
  });
}
