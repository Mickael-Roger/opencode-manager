import { expect, test } from "bun:test"
import { runLocalShell } from "../../src/features/composer/local-shell"

test("runs a shell command in the supplied working directory", async () => {
  const result = await runLocalShell("printf '%s' \"$PWD\"", "/tmp")

  expect(result).toEqual({ output: "/tmp", exitCode: 0 })
})

test("captures standard error and failed exit codes", async () => {
  const result = await runLocalShell("printf failure >&2; exit 7", process.cwd())

  expect(result).toEqual({ output: "failure", exitCode: 7 })
})
