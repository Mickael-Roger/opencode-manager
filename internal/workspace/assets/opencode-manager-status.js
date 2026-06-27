// opencode-manager-status.js
//
// Seeded and OWNED by opencode-manager. Do not edit by hand: it is overwritten
// with the shipped version every time opencode-manager starts.
//
// It observes the opencode event bus and writes a small status file that the
// opencode-manager TUI reads from the host to show a live, per-workspace
// activity dashboard (working / waiting on a human / sleeping / off).
//
// The file lives under the workspace home directory, which is bind-mounted on
// the host, so the manager reads it directly without any exec or network call:
//
//   $HOME/.local/state/opencode-manager/status.json

import { mkdirSync, writeFileSync, renameSync } from "node:fs"
import { dirname } from "node:path"

const HOME = process.env.HOME || process.env.USERPROFILE || "."
const STATE_FILE = `${HOME}/.local/state/opencode-manager/status.json`

// Higher wins when several sessions are active at once: a single session that
// needs approval should surface above others that are merely working.
const PRIORITY = {
  "needs-approval": 4,
  error: 3,
  working: 2,
  idle: 1,
}

export const OpencodeManagerStatus = async ({ directory }) => {
  // sessionID -> "working" | "idle" | "needs-approval" | "error"
  const sessions = {}

  const aggregate = () => {
    let best = "idle"
    let pending = 0
    let count = 0
    for (const id in sessions) {
      count++
      const s = sessions[id]
      if (s === "needs-approval") pending++
      if ((PRIORITY[s] || 0) > (PRIORITY[best] || 0)) best = s
    }
    return { activity: count === 0 ? "idle" : best, pendingApproval: pending, sessions: count }
  }

  const write = () => {
    const payload = {
      ...aggregate(),
      directory: directory || "",
      updatedAt: new Date().toISOString(),
    }
    try {
      mkdirSync(dirname(STATE_FILE), { recursive: true })
      const tmp = `${STATE_FILE}.tmp`
      writeFileSync(tmp, JSON.stringify(payload))
      renameSync(tmp, STATE_FILE) // atomic replace so the manager never reads a partial file
    } catch {
      // Best effort only: reporting status must never disrupt opencode.
    }
  }

  // Different events carry the session id in different shapes; try the common
  // ones and fall back to a single bucket so aggregation still works.
  const sessionOf = (event) => {
    const p = event?.properties || {}
    return p.sessionID || p.info?.sessionID || p.sessionId || "default"
  }

  // Initial heartbeat so a freshly started workspace shows up as live/idle.
  write()

  // Periodic heartbeat: refreshes updatedAt while opencode runs. When opencode
  // exits (e.g. the user only opened a shell in the container), the file goes
  // stale and the manager can tell the agent is no longer running.
  const timer = setInterval(write, 10000)
  if (timer && typeof timer.unref === "function") timer.unref()

  return {
    event: async ({ event }) => {
      const id = sessionOf(event)
      switch (event?.type) {
        case "message.updated": {
          // Only an in-progress assistant message means active generation.
          // The final message.updated of a turn carries time.completed and can
          // arrive AFTER session.idle; treating it as "working" would pin the
          // session to working forever even though opencode is idle. A user
          // message likewise is not the agent working.
          const info = event?.properties?.info
          if (info?.role === "assistant") {
            sessions[id] = info?.time?.completed ? "idle" : "working"
          }
          break
        }
        case "message.part.updated":
        case "tool.execute.before":
        case "tool.execute.after":
          sessions[id] = "working"
          break
        case "permission.asked":
        case "permission.updated":
          sessions[id] = "needs-approval"
          break
        case "permission.replied":
          sessions[id] = "working"
          break
        case "session.idle":
          sessions[id] = "idle"
          break
        case "session.error":
          sessions[id] = "error"
          break
        case "session.deleted":
          delete sessions[id]
          break
        default:
          return // ignore unrelated events without rewriting the file
      }
      write()
    },
  }
}
