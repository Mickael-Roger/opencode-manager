import { expect, test } from "bun:test"
import { expandTrackedPastes, pasteSummary } from "../../src/features/composer/paste"

test("summarizes multiline and large pastes", () => {
  expect(pasteSummary("one\ntwo\nthree")).toEqual({ label: "[Pasted ~3 lines]", text: "one\ntwo\nthree" })
  expect(pasteSummary("one\ntwo")).toBeUndefined()
})

test("expands tracked paste labels before prompt submission", () => {
  expect(expandTrackedPastes("Before [Pasted ~3 lines] after", [{ start: 7, end: 24, text: "one\ntwo\nthree" }])).toBe("Before one\ntwo\nthree after")
})
