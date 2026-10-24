// Installed by omaterm (`omaterm skill setup`), which replaces it as omaterm
// updates; `omaterm skill remove` removes it. OpenCode's service runs this
// for every terminal: it tells the agent of a session that runs in a omaterm
// pane which session and pane it runs in, as the pane's terminal plugin
// (tui.js) told omaterm.
//
// Adapted from Kero (github.com/egoist/kero, MIT), internal/cli/agents/opencode/index.js.
import { execFile } from "node:child_process"

const OMATERM = __OMATERM__

// lookup asks omaterm what to tell a session's agent; wait gives its pane a
// moment to tell omaterm the session, as when the session was just made.
function lookup(sessionID, wait) {
  return new Promise((resolve) => {
    const args = ["hook", "context", "opencode", sessionID, ...(wait ? ["wait"] : [])]
    const child = execFile(OMATERM, args, { timeout: 5000 }, (err, stdout) => {
      resolve(err ? "" : stdout.trim())
    })
    child.on("error", () => resolve(""))
  })
}

export default {
  id: "omaterm",
  async setup(ctx) {
    // What each session is told, once omaterm knows it; a session it doesn't
    // know is asked about a few times, as its pane may not have told yet.
    const known = new Map()
    await ctx.session.hook("context", async (event) => {
      let entry = known.get(event.sessionID)
      if (!entry) known.set(event.sessionID, (entry = { text: "", tries: 0 }))
      if (!entry.text && entry.tries < 5) {
        entry.tries++
        entry.text = await lookup(event.sessionID, entry.tries === 1)
      }
      if (entry.text) event.system.push({ type: "text", text: entry.text })
    })
  },
}
