import { expect, test } from "bun:test"
import { encodeQuestionAnswers } from "../../src/features/questions/answers"

test("encodes selected and free-text answers with their stable question IDs", () => {
  const request = {
    clientId: "client", eventId: "event", sessionId: "session", questions: [
      { id: "mode", question: "Choose", options: [{ label: "Safe" }] },
      { id: "details", question: "Explain" },
    ],
  }
  expect(encodeQuestionAnswers(request, { mode: ["Safe"] }, { details: "Use staging" })).toEqual([
    { id: "mode", selected: ["Safe"] },
    { id: "details", selected: [], custom: "Use staging" },
  ])
})
