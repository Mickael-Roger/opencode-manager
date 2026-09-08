import { realpath } from "node:fs/promises"
import { parseCli } from "./cli"
import { resolveLaunchUrl } from "./dsh/connection"
import { RemoteGateway } from "./dsh/remote-gateway"
import { startApp } from "./app"
import { bootstrap } from "./bootstrap"
import { registerGrammars } from "./ui/grammars"

const options = parseCli(process.argv)
try {
    const launchUrl = await resolveLaunchUrl(options.dshUrl)
    const gateway = await RemoteGateway.connect(launchUrl)
    const cwd = await realpath(options.cwd)
    const initial = await bootstrap(gateway, cwd, options)
    registerGrammars()
    await startApp({ cwd, gateway, initial, dshOrigin: new URL(launchUrl).origin })
} catch (error) {
  console.error(`dsh-tui: ${error instanceof Error ? error.message : String(error)}`)
  process.exitCode = 1
}
