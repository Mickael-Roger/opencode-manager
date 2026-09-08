import { expect, test } from "bun:test"
import { clipText } from "../../src/ui/clip"

test("returns full text when at or under the limit", () => {
  expect(clipText("one\ntwo\nthree")).toEqual({ visible: "one\ntwo\nthree", hidden: 0 })
  const ten = Array.from({ length: 10 }, (_, index) => `line ${index}`).join("\n")
  expect(clipText(ten)).toEqual({ visible: ten, hidden: 0 })
})

test("clips to the first ten lines and counts the remainder", () => {
  const twelve = Array.from({ length: 12 }, (_, index) => `line ${index}`).join("\n")
  expect(clipText(twelve)).toEqual({ visible: "line 0\nline 1\nline 2\nline 3\nline 4\nline 5\nline 6\nline 7\nline 8\nline 9", hidden: 2 })
})

test("normalizes line endings and ignores a single trailing newline", () => {
  expect(clipText("a\r\nb\r\nc\n")).toEqual({ visible: "a\nb\nc", hidden: 0 })
  expect(clipText("a\rb")).toEqual({ visible: "a\nb", hidden: 0 })
})
