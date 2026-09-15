export interface LocalShellResult {
  output: string
  exitCode: number
}

// Run through the user's shell so composer commands support pipes, redirects,
// and other shell syntax just as they do in a terminal.
export async function runLocalShell(command: string, cwd: string): Promise<LocalShellResult> {
  const shell = process.env.SHELL || "/bin/sh"
  const child = Bun.spawn([shell, "-lc", command], { cwd, stdout: "pipe", stderr: "pipe" })
  const [stdout, stderr, exitCode] = await Promise.all([child.stdout.text(), child.stderr.text(), child.exited])
  return { output: `${stdout}${stderr}`, exitCode }
}
