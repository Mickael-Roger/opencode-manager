import { expect, test } from "bun:test"
import { mkdtemp, readFile, writeFile } from "node:fs/promises"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { appendPromptHistory, PromptHistoryStore } from "../../src/prompt-history"

test("persists prompt history across store instances", async () => {
  const path = join(await mkdtemp(join(tmpdir(), "dsh-history-")), "history.json")
  const store = new PromptHistoryStore(path)
  store.save(["first", "second"])
  await store.flush()

  expect(await new PromptHistoryStore(path).load()).toEqual(["first", "second"])
  expect(JSON.parse(await readFile(path, "utf8"))).toEqual(["first", "second"])
})

test("ignores malformed history and retains the most recent 100 prompts", async () => {
  const path = join(await mkdtemp(join(tmpdir(), "dsh-history-")), "history.json")
  await writeFile(path, "not json")
  expect(await new PromptHistoryStore(path).load()).toEqual([])

  const history = Array.from({ length: 101 }, (_, index) => `prompt ${index}`)
  expect(appendPromptHistory(history, "next")).toEqual([...history.slice(-99), "next"])
  expect(appendPromptHistory(["same"], "same")).toEqual(["same"])
})
