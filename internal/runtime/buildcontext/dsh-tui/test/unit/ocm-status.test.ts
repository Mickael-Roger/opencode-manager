import { expect, test } from "bun:test"
import { statusPayload } from "../../src/ocm-status"

test("writes the manager status-file schema", () => {
  const payload = JSON.parse(statusPayload("needs-approval", 1))
  expect(payload).toMatchObject({ activity: "needs-approval", pendingApproval: 1, sessions: 1 })
  expect(new Date(payload.updatedAt).toString()).not.toBe("Invalid Date")
})
