# DSH API Notes

Test target: `@deepseek-ai/dsh@0.1.2-rc.1`.

## Connection

The published Cordis client packages are designed for a browser and derive their
origin and WebSocket transport from browser globals. No external Bun/Node
initialization API is published at this version. DSH Web accepts its launch token
only on `GET /?token=...`, responds with a `303`, and mints an authority-bound
HttpOnly cookie. Remote calls and streams must use that cookie, never the token.

`src/dsh/remote-gateway.ts` is the sole temporary fallback boundary for this
transport. It uses the generated Remote envelope and `/api/remote.mux` stream
carrier. Replace it with the official client when DSH publishes a non-browser
bootstrap.

## Confirmed Remote operations

- `session.list`, `session.create`, `session.modelCatalog`, `session.selectModel`
- `session.prompt`, `session.cancel`, `session.page`, `session.follow`
- `session.control` for live projection state
- `commands.list`, `commands.execute`
- `pluginInventory.list`
- session-aware file-reference listing and subagent catalog operations

`session.list` is unusual: its generated argument name is `_request`; other
session methods receive `request`. The transport adapter preserves that detail.

## Client behavior

`dsh-tui` uses `session.list`, `session.create`, `session.modelCatalog`,
`session.selectModel`, `session.prompt`, `session.cancel`, `session.follow`, and
`fileReferences/list`. Slash-command completion lists the selected Agent's
commands through `commands/list`, and command submission invokes
`commands/execute`. Its `@` completion is deliberately limited to the
published file-reference surface in this pinned release. The published Remote
contract inspected for `0.1.2-rc.1` did not provide a verified standalone
subagent-reference catalog, so the TUI does not invent one.

Context occupancy uses the `contextPressure` projection from the opening
`session/follow` snapshot and subsequent `session/control` projection frames.
It prefers `projectedTokens` over `pressureTokens` and applies the same rounded,
100%-bounded percentage calculation as DSH Web.

The optional MCP sidebar section reads `pluginInventory/list` and recognizes
Loader rows whose module is `@deepseek-ai/dsh-mcp-client`. Its dot represents
plugin activation (`enabled` and `fiberPhase`), because this Remote snapshot
does not expose the MCP transport's connection health.

The AgentTeams DAG overlay is gated on the same inventory listing
`@nanmicoder/dsh-agent-teams`. It reads the plugin's cookie-authenticated web
route `GET /plugins/dsh-agent-teams/state`, which returns `{ teams: [...] }`
with members, tasks (subject, assignee, dependencies, visual state), phase,
and captain session. Task layering is recomputed locally from dependencies.
