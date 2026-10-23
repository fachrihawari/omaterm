// Installed by omaterm (`omaterm skill setup`), which replaces it as omaterm
// updates; `omaterm skill remove` removes it. Inside omaterm, it tells omaterm the
// session this terminal shows, to resume it after omaterm's background
// service restarts, and to tell its agent where it runs (index.js).
import { execFile } from "node:child_process"

const OMATERM = __OMATERM__

function report(sessionID) {
  const child = execFile(OMATERM, ["hook", "opencode"], { timeout: 5000 }, () => {})
  child.on("error", () => {})
  child.stdin?.end(JSON.stringify({ hook_event_name: "SessionStart", session_id: sessionID }))
}

export default {
  id: "omaterm",
  setup(context) {
    if (!process.env.OMATERM_PANE) return
    // The session shown, or the one a subagent's session belongs to.
    const root = (id) => {
      const seen = new Set()
      while (typeof id === "string" && !seen.has(id)) {
        seen.add(id)
        const session = context.data.session.get(id)
        if (!session) return
        if (!session.parentID) return id
        id = session.parentID
      }
    }
    let reported
    const poll = setInterval(() => {
      const route = context.ui.router.current()
      const id = route?.type === "session" ? root(route.sessionID) : undefined
      if (id && id !== reported) {
        reported = id
        report(id)
      }
    }, 250)
    return () => clearInterval(poll)
  },
}
