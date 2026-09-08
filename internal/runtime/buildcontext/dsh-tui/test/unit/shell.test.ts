import { expect, test } from "bun:test"
import { shellFallback, shellHighlights } from "../../src/ui/shell"

test("colors commands, strings, variables, flags and operators", () => {
  expect(shellHighlights("echo \"hi\"")).toEqual([[0, 4, "function.call"], [5, 9, "string"]])
  expect(shellHighlights("kubectl get pods -n prod --watch")).toEqual([
    [0, 7, "function.call"],
    [17, 19, "attribute"],
    [25, 32, "attribute"],
  ])
  expect(shellHighlights("cd src && npm run check")).toEqual([
    [0, 2, "function.call"],
    [7, 9, "operator"],
    [10, 13, "function.call"],
  ])
})

test("colors shell keywords and environment assignments", () => {
  expect(shellHighlights("export FOO=bar")).toEqual([[0, 6, "function.call"], [7, 10, "property"]])
  expect(shellHighlights("VAR=prod kubectl get pods")).toEqual([[0, 3, "property"], [9, 16, "function.call"]])
  expect(shellHighlights("for x in a b; do echo $x; done")).toEqual([
    [0, 3, "keyword"], [6, 8, "keyword"], [12, 13, "operator"], [14, 16, "keyword"], [17, 21, "function.call"],
    [22, 24, "variable.bash"], [24, 25, "operator"], [26, 30, "keyword"],
  ])
})

test("colors comments, redirections and quoted strings", () => {
  expect(shellHighlights("# note\ncmd")).toEqual([[0, 6, "comment"], [7, 10, "function.call"]])
  expect(shellHighlights("go build ./cmd 2>&1 | tee out.log")).toEqual([
    [0, 2, "function.call"], [15, 19, "operator"], [20, 21, "operator"], [22, 25, "function.call"],
  ])
  expect(shellHighlights("if [ -f \"/tmp/x\" ]; then echo ok; fi")).toEqual([
    [0, 2, "keyword"], [8, 16, "string"], [18, 19, "operator"], [20, 24, "keyword"],
    [25, 29, "function.call"], [32, 33, "operator"], [34, 36, "keyword"],
  ])
})

test("fallback keeps tree-sitter highlights when present", () => {
  const context = { content: "echo hi", filetype: "bash", syntaxStyle: undefined as never }
  expect(shellFallback([[0, 4, "function.call"]], context)).toBeUndefined()
  expect(shellFallback([], context)).toEqual(shellHighlights("echo hi"))
})
