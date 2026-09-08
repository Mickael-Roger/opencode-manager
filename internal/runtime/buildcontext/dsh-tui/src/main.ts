import { realpath } from "node:fs/promises"
import { parseCli } from "./cli"
import { resolveLaunchUrl } from "./dsh/connection"
import { RemoteGateway } from "./dsh/remote-gateway"
import { startApp } from "./app"
import { bootstrap } from "./bootstrap"
import { OcmStatusReporter } from "./ocm-status"
import { PromptHistoryStore } from "./prompt-history"
import { registerGrammars } from "./ui/grammars"

const options = parseCli(process.argv)
const statusReporter = new OcmStatusReporter()
const promptHistoryStore = new PromptHistoryStore()
statusReporter.start()
try {
    const launchUrl = await resolveLaunchUrl(options.dshUrl)
    const gateway = await RemoteGateway.connect(launchUrl)
    const cwd = await realpath(options.cwd)
    const initial = await bootstrap(gateway, cwd, options)
    registerGrammars()
    await startApp({ cwd, gateway, initial, dshOrigin: new URL(launchUrl).origin, statusReporter, promptHistoryStore, initialPromptHistory: await promptHistoryStore.load() })
} catch (error) {
  statusReporter.set("error")
  console.error(`dsh-tui: ${error instanceof Error ? error.message : String(error)}`)
  process.exitCode = 1
} finally {
  await promptHistoryStore.flush()
  statusReporter.stop()
}
