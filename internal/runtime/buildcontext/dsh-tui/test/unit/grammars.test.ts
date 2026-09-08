import { expect, test } from "bun:test"
import { grammarSpecs, registerGrammars } from "../../src/ui/grammars"
import { queries } from "../../src/ui/queries"

test("every grammar has a wasm and highlight query", () => {
  for (const grammar of grammarSpecs) {
    expect(grammar.wasm.endsWith(".wasm")).toBe(true)
    expect(queries[grammar.query]?.trim().length).toBeGreaterThan(0)
  }
})

test("highlight query parentheses are balanced", () => {
  for (const query of Object.values(queries)) {
    expect(query.split("(").length).toBe(query.split(")").length)
  }
})

test("registration degrades safely when the package is unavailable", () => {
  expect(() => registerGrammars()).not.toThrow()
})
