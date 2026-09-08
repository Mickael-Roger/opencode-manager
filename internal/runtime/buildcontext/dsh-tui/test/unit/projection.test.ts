import { expect, test } from "bun:test"
import { contextOccupancy, expandFrames, isTurnFinished, projectFrame, snapshotContextPressure, updateCompaction } from "../../src/dsh/projection"

test("reads context pressure and calculates the bounded web-compatible occupancy", () => {
  const snapshot = { type: "snapshot", projections: { asOfSeq: 9, values: { contextPressure: { pressureTokens: 20_000, projectedTokens: 32_100, contextWindow: 128_000 } } } }
  expect(snapshotContextPressure(snapshot)).toEqual({ pressure: { pressureTokens: 20_000, projectedTokens: 32_100, contextWindow: 128_000 }, seq: 9 })
  expect(contextOccupancy(snapshotContextPressure(snapshot)?.pressure)).toEqual({ percent: 25, usedTokens: 32_100, contextWindow: 128_000 })
  expect(contextOccupancy({ pressureTokens: 150, contextWindow: 100 })?.percent).toBe(100)
  expect(contextOccupancy({ contextWindow: 100 })).toBeUndefined()
})

test("flattens follow snapshots and durable event envelopes", () => {
  const snapshot = {
    type: "snapshot",
    records: [{ type: "event", event: { type: "user/message", seq: 3, data: { content: [{ type: "text", text: "hello" }] } } }],
  }
  expect(expandFrames(snapshot)).toEqual([{ type: "user/message", seq: 3, data: { content: [{ type: "text", text: "hello" }] } }])
  expect(expandFrames({ type: "event", event: { type: "turn/end" } })).toEqual([{ type: "turn/end" }])
})

test("projects DSH nested user content and recognizes completed turns", () => {
  const nodes = projectFrame([], { type: "user/message", seq: 7, data: { content: [{ type: "text", text: "hello" }] } })
  expect(nodes).toEqual([{ id: "7", kind: "user", text: "hello", complete: true }])
  expect(isTurnFinished({ type: "turn/end" })).toBe(true)
})

test("tracks a compaction until its matching end event", () => {
  let active = updateCompaction(undefined, { type: "compaction/start", data: { compactionId: "compact-a" } })
  expect(active).toBe("compact-a")
  active = updateCompaction(active, { type: "compaction/end", data: { compactionId: "compact-b", error: "stale" } })
  expect(active).toBe("compact-a")
  active = updateCompaction(active, { type: "compaction/end", data: { compactionId: "compact-a" } })
  expect(active).toBeUndefined()
})

test("ignores internal DSH bookkeeping events", () => {
  expect(projectFrame([], { type: "model/selection", seq: 1 })).toEqual([])
  expect(projectFrame([], { type: "turn/start", seq: 2 })).toEqual([])
  expect(projectFrame([], { type: "agent/inbox/spliced", seq: 3 })).toEqual([])
})

test("reconciles text deltas, block end, and durable assistant message", () => {
  let nodes = projectFrame([], { type: "assistant/chunk", seq: 1, data: { turn: 2, step: 1, chunk: { type: "text-delta", text: "Hello" } } })
  nodes = projectFrame(nodes, { type: "assistant/chunk", seq: 2, data: { turn: 2, step: 1, chunk: { type: "block-end", block: { type: "text", text: "Hello" } } } })
  nodes = projectFrame(nodes, { type: "assistant/message", seq: 3, data: { turn: 2, step: 1, message: { content: [{ type: "text", text: "Hello" }] } } })
  expect(nodes).toEqual([{ id: "assistant:2:1", kind: "assistant", text: "Hello", complete: true }])
})

test("pairs tool calls and results while preserving arguments and output", () => {
  let nodes = projectFrame([], { type: "tool/call", seq: 10, data: { callId: "call-1", name: "bash", arguments: '{"command":"pwd"}' } })
  nodes = projectFrame(nodes, { type: "tool/result", seq: 11, data: { message: { source: { kind: "tool", callId: "call-1" }, content: [{ type: "tool-result", content: [{ type: "text", text: "/workspace\n" }], isError: false }] } } })
  expect(nodes).toEqual([{ id: "tool:call-1", kind: "tool", text: "bash", toolName: "bash", toolArgs: { command: "pwd" }, toolOutput: "/workspace\n", isError: false, complete: true }])
})
