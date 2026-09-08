import { randomUUID } from "node:crypto"
import { authenticateLaunchUrl, type AuthenticatedConnection } from "./connection"
import type { DshGateway } from "./gateway"
import type { AgentTeamSnapshot, ApprovalEvent, CommandDescriptor, CommandExecution, ContextPressure, ContextPressureUpdate, FileReference, ModelCatalog, ModelSelection, PluginInventorySnapshot, SessionFrame, SessionHandle, SessionSummary } from "./types"

interface RemoteEnvelope<T> {
  type: "server-response"
  rpcId: string
  result: { ok: true; value: T } | { ok: false; error: { code: string; message: string } }
}

// This is the narrowly scoped fallback for DSH 0.1.2-rc.1. Its published Cordis
// client assumes browser globals and has no external Bun bootstrap. Keep all wire
// paths here so it can be replaced by the official client when that surface exists.
export class RemoteGateway implements DshGateway {
  private constructor(private readonly connection: AuthenticatedConnection) {}

  static async connect(launchUrl: string): Promise<RemoteGateway> {
    return new RemoteGateway(await authenticateLaunchUrl(launchUrl))
  }

  async listSessions(): Promise<readonly SessionSummary[]> {
    return (await this.call<{ items: SessionSummary[] }>("session/list", { _request: {} })).items
  }

  async createSession(cwd: string, sessionId?: string): Promise<SessionHandle> {
    return this.call("session/create", { request: { cwd, ...(sessionId ? { sessionId } : {}) } })
  }

  getModelCatalog(): Promise<ModelCatalog> {
    return this.call("session/modelCatalog", {})
  }

  async selectModel(sessionId: string, selection: ModelSelection): Promise<ModelSelection> {
    return (await this.call<{ selected: ModelSelection }>("session/selectModel", { request: { sessionId, ...selection } })).selected
  }

  async sendPrompt(sessionId: string, text: string): Promise<void> {
    await this.call("session/prompt", { request: {
      requestId: randomUUID(), sessionId, mode: "queue", content: [{ type: "text", text }],
    } })
  }

  async cancel(sessionId: string): Promise<void> {
    await this.call("session/cancel", { request: { sessionId } })
  }

  listCommands(sessionId: string): Promise<readonly CommandDescriptor[]> {
    return this.call("commands/list", { agentId: sessionId })
  }

  executeCommand(sessionId: string, line: string): Promise<CommandExecution | undefined> {
    return this.call("commands/execute", { agentId: sessionId, line, images: [] })
  }

  getPluginInventory(): Promise<PluginInventorySnapshot> {
    return this.call("pluginInventory/list", {})
  }

  async getAgentTeams(): Promise<readonly AgentTeamSnapshot[]> {
    // Plugin-owned web route (cookie-authenticated plain JSON), not the Remote
    // RPC carrier.
    const response = await fetch(new URL("/plugins/dsh-agent-teams/state", this.connection.origin), {
      headers: { Cookie: this.connection.cookie },
      signal: AbortSignal.timeout(15_000),
    })
    if (!response.ok) throw new Error(`DSH agent-teams state failed: HTTP ${response.status}`)
    return ((await response.json()) as { teams: AgentTeamSnapshot[] }).teams
  }

  completeFileReferences(sessionId: string, query: string): Promise<readonly FileReference[]> {
    return this.call("fileReferences/list", { agentId: sessionId, query })
  }

  async *followSession(sessionId: string, signal: AbortSignal): AsyncIterable<SessionFrame> {
    yield* this.stream("session/follow", { request: { address: { kind: "session", sessionId } } }, signal)
  }

  async *followContextPressure(signal: AbortSignal): AsyncIterable<ContextPressureUpdate> {
    for await (const frame of this.stream<Record<string, unknown>>("session/control", {}, signal)) {
      if (frame.type === "baseline") {
        const projections = (frame.value as Record<string, unknown> | undefined)?.projections as Record<string, Record<string, unknown>> | undefined
        for (const [sessionId, block] of Object.entries(projections ?? {})) {
          const pressure = (block.values as Record<string, unknown> | undefined)?.contextPressure
          if (pressure && typeof pressure === "object") yield { sessionId, pressure: pressure as ContextPressure, seq: Number(block.asOfSeq ?? 0) }
        }
      } else if (frame.type === "projection" && frame.key === "contextPressure" && typeof frame.sessionId === "string" && frame.value && typeof frame.value === "object") {
        yield { sessionId: frame.sessionId, pressure: frame.value as ContextPressure, seq: Number(frame.seq ?? 0) }
      }
    }
  }

  async *followApprovals(signal: AbortSignal): AsyncIterable<ApprovalEvent> {
    let clientId: string | undefined
    for await (const value of this.stream<Record<string, unknown>>("$events", {}, signal)) {
      if (value.type === "ready" && typeof value.clientId === "string") clientId = value.clientId
      else if (value.type === "waterfall" && value.event === "approval/request" && clientId) {
        const request = value.request as Record<string, unknown>
        yield { type: "request", request: {
          clientId, eventId: String(value.eventId), sessionId: String(value.agentId),
          toolName: String(request.toolName), callId: typeof request.callId === "string" ? request.callId : undefined,
          reason: typeof request.reason === "string" ? request.reason : undefined,
        } }
      } else if (value.type === "cancel" && typeof value.eventId === "string") yield { type: "cancel", eventId: value.eventId }
    }
  }

  async answerApproval(clientId: string, eventId: string, decision: "allowed-once" | "rejected"): Promise<void> {
    await this.call("$events/result", { clientId, eventId, outcome: { kind: "result", value: decision } })
  }

  private async *stream<T>(endpoint: string, args: Record<string, unknown>, signal: AbortSignal): AsyncIterable<T> {
    const url = new URL("/api/remote.mux", this.connection.origin)
    url.protocol = url.protocol === "https:" ? "wss:" : "ws:"
    // Bun supports request headers here, but its DOM-compatible declaration does
    // not expose the Bun extension yet.
    const socket = new WebSocket(url, { headers: { Cookie: this.connection.cookie } } as unknown as string[])
    const streamId = randomUUID()
    const frames: T[] = []
    let wake: (() => void) | undefined
    let ended = false
    let failure: Error | undefined
    socket.onmessage = event => {
      let frame: { type: string; streamId?: string; value?: T; error?: { message: string } }
      try { frame = JSON.parse(String(event.data)) as typeof frame } catch { return }
      if (frame.streamId !== streamId) return
      if (frame.type === "item" && frame.value) {
        if (frames.length >= 1_000) { failure = new Error("DSH stream fell behind; reconnect to recover history"); ended = true }
        else frames.push(frame.value)
      }
      if (frame.type === "end") ended = true
      if (frame.type === "error") { failure = new Error(frame.error?.message ?? "DSH stream failed"); ended = true }
      wake?.()
    }
    socket.onclose = () => { ended = true; wake?.() }
    const abort = () => { ended = true; socket.close(); wake?.() }
    signal.addEventListener("abort", abort, { once: true })
    await new Promise<void>((resolve, reject) => {
      const timeout = setTimeout(() => reject(new Error("Timed out connecting to DSH stream")), 10_000)
      socket.onopen = () => {
        socket.send(JSON.stringify({ type: "open", streamId, endpoint, payload: { args } }))
        clearTimeout(timeout)
        resolve()
      }
      socket.onerror = () => { clearTimeout(timeout); reject(new Error("DSH WebSocket connection failed")) }
    })
    try {
      while (!ended || frames.length) {
        if (signal.aborted) break
        if (frames.length) yield frames.shift()!
        else await new Promise<void>(resolve => { wake = resolve })
        wake = undefined
      }
      if (failure) throw failure
    } finally {
      signal.removeEventListener("abort", abort)
      socket.close()
    }
  }

  private async call<T>(path: string, args: Record<string, unknown>): Promise<T> {
    const rpcId = randomUUID()
    const timeout = AbortSignal.timeout(15_000)
    const response = await fetch(new URL(`/api/${path}`, this.connection.origin), {
      method: "POST",
      headers: { "content-type": "application/json", Cookie: this.connection.cookie },
      body: JSON.stringify({
        type: "client-request",
        rpcId,
        method: path,
        payload: { args },
      }),
      signal: timeout,
    })
    if (!response.ok) throw new Error(`DSH Remote ${path} failed: HTTP ${response.status}`)
    const body = await response.json() as RemoteEnvelope<T>
    if (!body.result.ok) throw new Error(`DSH Remote ${path} failed: ${body.result.error.code}: ${body.result.error.message}`)
    return body.result.value
  }
}
