import { parseArgs } from "node:util"
import { resolveLaunchUrl } from "../src/dsh/connection"
import { expandFrames, isTurnFinished } from "../src/dsh/projection"
import { RemoteGateway } from "../src/dsh/remote-gateway"

const { values } = parseArgs({ options: { "dsh-url": { type: "string" }, cwd: { type: "string", default: process.cwd() } } })
const launchUrl = await resolveLaunchUrl(values["dsh-url"])

const gateway = await RemoteGateway.connect(launchUrl)
const sessions = await gateway.listSessions()
const catalog = await gateway.getModelCatalog()
console.log("Connected to DSH")
console.log(`Sessions: ${sessions.length}`)
console.log(`Providers: ${catalog.groups.map(group => group.name).join(", ")}`)
console.log(`Models: ${catalog.groups.flatMap(group => group.models.map(model => `${group.id}/${model.id}`)).join(", ")}`)

const session = await gateway.createSession(values.cwd!)
await gateway.sendPrompt(session.sessionId, "reply with OK")
const controller = new AbortController()
for await (const frame of gateway.followSession(session.sessionId, controller.signal)) {
  for (const event of expandFrames(frame)) {
    console.log(JSON.stringify(event))
    if (isTurnFinished(event)) {
      controller.abort()
      break
    }
  }
  if (controller.signal.aborted) break
}
console.log(`File references: ${(await gateway.completeFileReferences(session.sessionId, "")).length}`)
