import { mkdir, rename, writeFile } from "node:fs/promises"
import { dirname, join } from "node:path"

export type OcmActivity = "starting" | "working" | "needs-approval" | "idle" | "error" | "off"

export function statusPayload(activity: OcmActivity, pendingApproval = 0): string {
  return JSON.stringify({ activity, pendingApproval, sessions: 1, updatedAt: new Date().toISOString() })
}

// DSH has no manager-facing activity capability. dsh-tui owns this best-effort
// heartbeat while it is attached, using the same bind-mounted state directory
// and JSON fields as OCM's OpenCode status plugin.
export class OcmStatusReporter {
  private readonly path = join(process.env.HOME ?? ".", ".local", "state", "opencode-manager", "deepseek-status.json")
  private activity: OcmActivity = "starting"
  private pendingApproval = 0
  private timer: ReturnType<typeof setInterval> | undefined
  private writes = Promise.resolve()

  start(): void {
    this.publish()
    this.timer = setInterval(() => this.publish(), 10_000)
  }

  set(activity: OcmActivity, pendingApproval = 0): void {
    this.activity = activity
    this.pendingApproval = pendingApproval
    this.publish()
  }

  stop(): void {
    if (this.timer) clearInterval(this.timer)
    this.timer = undefined
    this.set("off")
  }

  private publish(): void {
    const body = statusPayload(this.activity, this.pendingApproval)
    this.writes = this.writes.then(async () => {
      await mkdir(dirname(this.path), { recursive: true })
      const temporary = `${this.path}.tmp`
      await writeFile(temporary, body, { mode: 0o600 })
      await rename(temporary, this.path)
    }).catch(() => {})
  }
}
