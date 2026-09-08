import { Command } from "commander"

export interface CliOptions {
  continueSession: boolean
  session?: string
  model?: string
  cwd: string
  dshUrl?: string
  debug: boolean
}

export function parseCli(argv: string[]): CliOptions {
  const program = new Command()
    .name("dsh-tui")
    .description("Terminal UI for DeepSeek Harness")
    .option("-c, --continue", "continue the latest session for this workspace")
    .option("-s, --session <id>", "open a specific session")
    .option("-m, --model <provider/model>", "select a model for a new session")
    .option("-C, --cwd <path>", "workspace directory", process.cwd())
    .option("--dsh-url <url>", "DSH Web launch URL including its token")
    .option("--debug", "enable diagnostic logging")
  program.parse(argv)
  const options = program.opts<{ continue?: boolean; session?: string; model?: string; cwd: string; dshUrl?: string; debug?: boolean }>()
  return {
    continueSession: options.continue ?? false,
    session: options.session,
    model: options.model,
    cwd: options.cwd,
    dshUrl: options.dshUrl,
    debug: options.debug ?? false,
  }
}
