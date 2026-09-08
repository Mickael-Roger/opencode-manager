import type { ContextPressure, ConversationNode, SessionFrame } from "./types"

export interface ContextOccupancy {
  percent: number
  usedTokens: number
  contextWindow: number
}

export function contextOccupancy(pressure: ContextPressure | undefined): ContextOccupancy | undefined {
  const usedTokens = pressure?.projectedTokens ?? pressure?.pressureTokens
  if (usedTokens === undefined || pressure?.contextWindow === undefined) return
  return { percent: Math.min(100, Math.round(usedTokens / pressure.contextWindow * 100)), usedTokens, contextWindow: pressure.contextWindow }
}

export function snapshotContextPressure(frame: SessionFrame): { pressure: ContextPressure; seq: number } | undefined {
  if (frame.type !== "snapshot") return
  const projections = frame.projections as Record<string, unknown> | undefined
  const pressure = (projections?.values as Record<string, unknown> | undefined)?.contextPressure
  if (!pressure || typeof pressure !== "object") return
  return { pressure: pressure as ContextPressure, seq: Number(projections?.asOfSeq ?? 0) }
}

function textFrom(value: unknown): string | undefined {
  if (typeof value === "string") return value
  if (Array.isArray(value)) return value.map(textFrom).filter(Boolean).join("")
  if (value && typeof value === "object") {
    const record = value as Record<string, unknown>
    return textFrom(record.text) ?? textFrom(record.content) ?? textFrom(record.delta)
  }
}

function parseArguments(value: unknown): Record<string, unknown> {
  if (typeof value !== "string") return {}
  try { const parsed = JSON.parse(value); return parsed && typeof parsed === "object" ? parsed as Record<string, unknown> : {} } catch { return { value } }
}

function resultContent(payload: Record<string, unknown>): { text: string; isError: boolean } {
  const message = payload.message as Record<string, unknown> | undefined
  const result = Array.isArray(message?.content) ? message.content.find(item => (item as Record<string, unknown>).type === "tool-result") as Record<string, unknown> | undefined : undefined
  return { text: textFrom(result?.content) ?? "", isError: result?.isError === true }
}

// DSH emits many bookkeeping events. The conversation only projects content a
// user would see in the Web client; unknown events remain safely ignored.
export function projectFrame(nodes: readonly ConversationNode[], frame: SessionFrame): ConversationNode[] {
  const data = frame as Record<string, unknown>
  const payload = data.data && typeof data.data === "object" ? data.data as Record<string, unknown> : data
  const type = frame.type.toLowerCase()
  const source = payload.source as Record<string, unknown> | undefined
  if (type === "user/message" && source?.kind && source.kind !== "user") return [...nodes]
  const chunk = payload.chunk as Record<string, unknown> | undefined
  const text = textFrom(payload.message) ?? textFrom(payload.content) ?? textFrom(payload.delta) ?? textFrom(payload.text)
  const id = String(frame.seq ?? payload.messageId ?? payload.id ?? `${frame.type}:${nodes.length}`)
  if (type === "assistant/chunk") {
    if (chunk?.type !== "text-delta" || typeof chunk.text !== "string") return [...nodes]
    const streamId = `assistant:${String(payload.turn ?? "")}:${String(payload.step ?? "")}`
    const index = nodes.findIndex(node => node.id === streamId)
    if (index >= 0) return nodes.map((node, current) => current === index ? { ...node, text: node.text + chunk.text } : node)
    return [...nodes, { id: streamId, kind: "assistant", text: chunk.text, complete: false }]
  }
  if (type === "assistant/message" && text) {
    const streamId = `assistant:${String(payload.turn ?? "")}:${String(payload.step ?? "")}`
    const index = nodes.findIndex(node => node.id === streamId)
    if (index >= 0) return nodes.map((node, current) => current === index ? { ...node, text, complete: true } : node)
    return [...nodes, { id, kind: "assistant", text, complete: true }]
  }
  if (type.includes("assistant")) return [...nodes]
  if (type.includes("user") && text) return [...nodes, { id, kind: "user", text, complete: true }]
  if (type === "tool/call") {
    const callId = String(payload.callId ?? id)
    const name = String(payload.name ?? "tool")
    const args = parseArguments(payload.arguments)
    return [...nodes, { id: `tool:${callId}`, kind: "tool", text: name, toolName: name, toolArgs: args, complete: false }]
  }
  if (type === "tool/result") {
    const source = (payload.message as Record<string, unknown> | undefined)?.source as Record<string, unknown> | undefined
    const callId = String(source?.callId ?? id)
    const result = resultContent(payload)
    const index = nodes.findIndex(node => node.id === `tool:${callId}`)
    if (index >= 0) return nodes.map((node, current) => current === index ? { ...node, toolOutput: result.text, isError: result.isError, complete: true } : node)
    return [...nodes, { id: `tool:${callId}`, kind: "tool", text: "tool", toolOutput: result.text, isError: result.isError, complete: true }]
  }
  return [...nodes]
}

// session.follow sends an initial snapshot and then wraps durable log entries in
// { type: "event", event }. Flatten both shapes before the renderer sees them.
export function expandFrames(frame: SessionFrame): SessionFrame[] {
  if (frame.type === "event" && frame.event && typeof frame.event === "object") return [frame.event as SessionFrame]
  if (frame.type === "snapshot" && Array.isArray(frame.records)) {
    return frame.records.flatMap(record => {
      if (!record || typeof record !== "object") return []
      const event = (record as Record<string, unknown>).event
      return event && typeof event === "object" ? [event as SessionFrame] : []
    })
  }
  return [frame]
}

export function isTurnFinished(frame: SessionFrame): boolean {
  return frame.type === "turn/end" || frame.type === "turn/cancel"
}

// Compaction may run between turns, so it cannot be inferred from turn state.
// Keep the ID to avoid a stale end event clearing a newer compaction.
export function updateCompaction(active: string | undefined, frame: SessionFrame): string | undefined {
  const data = frame.data as Record<string, unknown> | undefined
  const id = data?.compactionId
  if (typeof id !== "string") return active
  if (frame.type === "compaction/start") return id
  if (frame.type === "compaction/end" && active === id) return undefined
  return active
}
