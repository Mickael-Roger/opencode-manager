import { expect, test } from "bun:test"
import { completeMention, findActiveMention, replaceMention } from "../../src/features/composer/mention"
import { rankReferences } from "../../src/features/composer/ranking"

test("finds only the mention under the cursor", () => {
  expect(findActiveMention("two @one and @tw", 16)).toEqual({ start: 13, end: 16, query: "tw" })
  expect(findActiveMention("two @one and @tw", 9)).toBeUndefined()
})

test("replaces the active mention without changing surrounding text", () => {
  const mention = findActiveMention("Review @src/a with @ar", 21)!
  expect(replaceMention("Review @src/a with @ar", mention, "@architect")).toEqual({ text: "Review @src/a with @architect", cursorOffset: 29 })
})

test("completes a mention at the cursor with one trailing space", () => {
  const mention = findActiveMention("Review @src/a", 13)!
  expect(completeMention("Review @src/a", mention, "@src/api.ts")).toEqual({ text: "Review @src/api.ts ", cursorOffset: 19 })

  const followedByWhitespace = findActiveMention("Review @src/a next", 13)!
  expect(completeMention("Review @src/a next", followedByWhitespace, "@src/api.ts")).toEqual({ text: "Review @src/api.ts next", cursorOffset: 18 })
})

test("ranks exact and prefix references before fuzzy matches", () => {
  const ranked = rankReferences([
    { kind: "file", label: "src/api/client.ts", insertText: "@src/api/client.ts" },
    { kind: "subagent", label: "api", insertText: "@api" },
    { kind: "directory", label: "app/config", insertText: "@app/config" },
  ], "api")
  expect(ranked.map(item => item.label)).toEqual(["api", "src/api/client.ts", "app/config"])
})
