import { createDefaultOpenTuiKeymap } from "@opentui/keymap/opentui"
import { registerLeader } from "@opentui/keymap/addons"
import { KeymapProvider, useBindings } from "@opentui/keymap/solid"
import { SyntaxStyle, pathToFiletype, type KeyEvent, type PasteEvent, type ScrollBoxRenderable, type TextareaRenderable } from "@opentui/core"
import { render, useKeyboard, useRenderer, useTerminalDimensions } from "@opentui/solid"
import { createEffect, createSignal, For, onCleanup, Show } from "solid-js"
import type { InitialState } from "./bootstrap"
import type { DshGateway } from "./dsh/gateway"
import { contextOccupancy, expandFrames, isTurnFinished, projectFrame, snapshotContextPressure } from "./dsh/projection"
import type { OcmStatusReporter } from "./ocm-status"
import { appendPromptHistory, type PromptHistoryStore } from "./prompt-history"
import type { ApprovalRequest, CommandDescriptor, ContextPressure, ConversationNode, ModelSelection, SessionSummary } from "./dsh/types"
import { findActiveMention, replaceMention } from "./features/composer/mention"
import { expandTrackedPastes, pasteSummary, type TrackedPaste } from "./features/composer/paste"
import { rankReferences, type ReferenceCandidate } from "./features/composer/ranking"
import { commandCandidates, commandLabel, commandName, completeCommand, type CommandCandidate } from "./features/commands/commands"
import type { McpStatus } from "./features/mcp/status"
import { modelOptions, modelSelection, type ModelOption, type ReasoningEffortOption } from "./features/model/options"
import { newestWorkspaceSession } from "./features/session/continue"
import { teamPanelLines } from "./features/teams/dag"
import { clipText } from "./ui/clip"
import { renderMarkdownNode } from "./ui/markdown"
import { shellFallback } from "./ui/shell"
import { createSyntaxStyle } from "./ui/theme"

const theme = { bg: "#0c0c0c", sidebar: "#151515", panel: "#1d1d1d", panelActive: "#262626", accent: "#5da9ff", text: "#e5e5e5", muted: "#777777", good: "#86d993", warn: "#e9b872", error: "#ed8796" }
const dshLogo = ["██████╗ ███████╗██╗  ██╗", "██╔══██╗██╔════╝██║  ██║", "██║  ██║███████╗███████║", "██║  ██║╚════██║██╔══██║", "██████╔╝███████║██║  ██║", "╚═════╝ ╚══════╝╚═╝  ╚═╝"]
type Overlay = "sessions" | "models" | "reasoning" | "references" | "help" | "commands" | "team" | undefined
export interface AppProps { cwd: string; dshOrigin: string; gateway: DshGateway; initial: InitialState; statusReporter: OcmStatusReporter; promptHistoryStore: PromptHistoryStore; initialPromptHistory: string[] }

function modelLabel(model?: ModelSelection): string { return model ? `${model.provider}/${model.model}${model.reasoningEffort ? ` (${model.reasoningEffort})` : ""}` : "default" }
function sessionLabel(session: SessionSummary): string { return session.title ?? session.sessionId }

function KeymapRoot(props: AppProps) {
  const renderer = useRenderer()
  const keymap = createDefaultOpenTuiKeymap(renderer)
  onCleanup(registerLeader(keymap, { trigger: "ctrl+x" }))
  return <KeymapProvider keymap={keymap}><App {...props} /></KeymapProvider>
}

export function App(props: AppProps) {
  const renderer = useRenderer()
  const dimensions = useTerminalDimensions()
  const commandFlags = { agentTeams: props.initial.agentTeams }
  const [sessions, setSessions] = createSignal(props.initial.sessions)
  const [sessionId, setSessionId] = createSignal(props.initial.sessionId)
  const [activeModel, setActiveModel] = createSignal<ModelSelection>(props.initial.model)
  const [nodes, setNodes] = createSignal<ConversationNode[]>([])
  const [draft, setDraft] = createSignal("")
  const [promptHistory, setPromptHistory] = createSignal(props.initialPromptHistory)
  const [overlay, setOverlay] = createSignal<Overlay>()
  const [selected, setSelected] = createSignal(0)
  const [pendingModel, setPendingModel] = createSignal<ModelOption>()
  const [references, setReferences] = createSignal<ReferenceCandidate[]>([])
  const [remoteCommands, setRemoteCommands] = createSignal<readonly CommandDescriptor[]>(props.initial.commands)
  const [commandOptions, setCommandOptions] = createSignal<readonly CommandCandidate[]>(commandCandidates(props.initial.commands, commandFlags))
  const [running, setRunning] = createSignal(false)
  const [status, setStatus] = createSignal<"connected" | "reconnecting">("connected")
  const [error, setError] = createSignal<string>()
  const [approval, setApproval] = createSignal<ApprovalRequest>()
  const [approvalPending, setApprovalPending] = createSignal(false)
  const [approvalChoice, setApprovalChoice] = createSignal(0)
  const [title, setTitle] = createSignal(sessionLabel(props.initial.sessions.find(item => item.sessionId === props.initial.sessionId) ?? { sessionId: props.initial.sessionId, updatedAt: 0, running: false, blank: false }))
  const [tokens, setTokens] = createSignal(0)
  const [contextPressures, setContextPressures] = createSignal<Record<string, { pressure: ContextPressure; seq: number }>>({})
  const [teamLines, setTeamLines] = createSignal<readonly string[]>([])
  let editor: TextareaRenderable | undefined
  let transcript: ScrollBoxRenderable | undefined
  const syntax = createSyntaxStyle()
  onCleanup(() => syntax.destroy())

  const models = (): readonly ModelOption[] => modelOptions(props.initial.catalog)
  const reasoningEfforts = () => pendingModel()?.reasoningEfforts ?? []
  const occupancy = () => contextOccupancy(contextPressures()[sessionId()]?.pressure)
  createEffect(() => {
    props.statusReporter.set(
      approval() ? "needs-approval" : status() === "reconnecting" ? "starting" : running() ? "working" : "idle",
      approval() ? 1 : 0,
    )
  })
  const refreshSessions = async () => setSessions(await props.gateway.listSessions())
  const refreshCommands = async (id: string) => {
    try {
      const next = await props.gateway.listCommands(id)
      if (sessionId() !== id) return
      setRemoteCommands(next)
      setCommandOptions(commandCandidates(next, commandFlags))
    } catch (cause) { if (sessionId() === id) setError(`Failed to load commands: ${String(cause)}`) }
  }
  const openSession = (id: string) => { setSessionId(id); setTitle(sessionLabel(sessions().find(item => item.sessionId === id) ?? { sessionId: id, updatedAt: 0, running: false, blank: false })); setNodes([]); setRemoteCommands([]); setCommandOptions(commandCandidates([], commandFlags)); setOverlay(undefined); setSelected(0); setStatus("connected"); void refreshCommands(id) }
  const createSession = async () => { openSession((await props.gateway.createSession(props.cwd)).sessionId); await refreshSessions() }
  const openOverlay = (value: Overlay) => { setSelected(0); setOverlay(value) }
  const openTeamDag = async () => {
    try {
      const teams = await props.gateway.getAgentTeams()
      const mine = teams.filter(team => team.captainSessionId === sessionId())
      setTeamLines(teamPanelLines(mine.length ? mine : teams))
      openOverlay("team")
    } catch (cause) { setError(`Failed to load team: ${String(cause)}`) }
  }
  const chooseModel = async (model: ModelSelection) => { setActiveModel(await props.gateway.selectModel(sessionId(), model)); setPendingModel(undefined); setOverlay(undefined) }
  const selectModelOption = async (model: ModelOption) => {
    if (!model.reasoningEfforts.length) { await chooseModel(modelSelection(model)); return }
    setPendingModel(model); setSelected(0); setOverlay("reasoning")
  }
  const overlayLength = () => overlay() === "sessions" ? sessions().length : overlay() === "models" ? models().length : overlay() === "reasoning" ? reasoningEfforts().length : overlay() === "references" ? references().length : overlay() === "commands" ? commandOptions().length : overlay() === "team" ? teamLines().length : 1
  const move = (delta: number) => setSelected(value => Math.max(0, Math.min(overlayLength() - 1, value + delta)))

  const acceptOverlay = async () => {
    if (overlay() === "sessions") { const item = sessions()[selected()]; if (item) openSession(item.sessionId) }
    else if (overlay() === "models") { const item = models()[selected()]; if (item) await selectModelOption(item) }
    else if (overlay() === "reasoning") {
      const model = pendingModel(); const effort = reasoningEfforts()[selected()]
      if (model && effort) await chooseModel(modelSelection(model, effort))
    }
    else if (overlay() === "references") {
      const item = references()[selected()]
      const mention = findActiveMention(draft(), draft().length)
      if (item && mention) {
        const next = replaceMention(draft(), mention, item.insertText).text
        setDraft(next); editor?.setText(next); setOverlay(undefined)
      }
    } else if (overlay() === "commands") {
      const command = commandOptions()[selected()]
      if (!command) return
      setOverlay(undefined)
      if (command.source === "remote" && command.input) {
        const next = `/${command.name} `
        setDraft(next)
        if (editor) { editor.setText(next); editor.cursorOffset = next.length }
      } else { setDraft(""); editor?.clear(); await runCommand(`/${command.name}`) }
    } else if (overlay() === "team") { await openTeamDag() }
  }
  const runCommand = async (text: string) => {
    const name = commandName(text)
    if (name === "sessions") openOverlay("sessions")
    else if (name === "model") openOverlay("models")
    else if (name === "new") await createSession()
    else if (name === "continue") { const item = newestWorkspaceSession(await props.gateway.listSessions(), props.cwd); item ? openSession(item.sessionId) : await createSession() }
    else if (name === "help") openOverlay("help")
    else if (name === "quit") renderer.destroy()
    else if (name === "agent-teams-dag") { if (props.initial.agentTeams) await openTeamDag(); else setError("The agent-teams plugin is not loaded") }
    else {
      try {
        const execution = await props.gateway.executeCommand(sessionId(), text)
        if (!execution) setError(`Unknown or malformed command: ${text}`)
        else if (execution.result.kind === "error") setError(execution.result.text)
        else setError(undefined)
      } catch (cause) { setError(`Command failed: ${String(cause)}`) }
    }
  }
  const submit = async (value = draft()) => {
    const text = value.trim()
    if (!text || overlay()) return
    const nextHistory = appendPromptHistory(promptHistory(), text)
    setPromptHistory(nextHistory)
    props.promptHistoryStore.save(nextHistory)
    if (text.startsWith("/")) { setDraft(""); editor?.clear(); await runCommand(text); return }
    if (running()) return
    setDraft(""); editor?.clear(); setRunning(true); setError(undefined)
    try { await props.gateway.sendPrompt(sessionId(), text) } catch (cause) { setError(String(cause)); setRunning(false) }
  }

  createEffect(() => {
    const id = sessionId()
    const controller = new AbortController()
    void (async () => {
      try {
        for await (const frame of props.gateway.followSession(id, controller.signal)) {
          const pressure = snapshotContextPressure(frame)
          if (pressure) setContextPressures(current => pressure.seq >= (current[id]?.seq ?? -1) ? { ...current, [id]: pressure } : current)
          for (const event of expandFrames(frame)) {
            setNodes(current => projectFrame(current, event))
            if (event.type === "turn/start") setRunning(true)
            if (event.type === "session/title") {
              const next = (event.data as Record<string, unknown> | undefined)?.title
              if (typeof next === "string") setTitle(next)
            }
            if (event.type === "assistant/chunk") {
              const usage = ((event.data as Record<string, unknown> | undefined)?.chunk as Record<string, unknown> | undefined)?.usage as Record<string, unknown> | undefined
              if (usage) setTokens(Number(usage.totalTokens ?? 0))
            }
            if (event.type === "request/header") {
              const model = ((event.data as Record<string, unknown> | undefined)?.header as Record<string, unknown> | undefined)?.config as ModelSelection | undefined
              if (model?.provider && model?.model) setActiveModel(model)
            }
            if (isTurnFinished(event)) setRunning(false)
          }
        }
      } catch (cause) { if (!controller.signal.aborted) { setStatus("reconnecting"); setError(`Stream disconnected: ${String(cause)}`) } }
    })()
    onCleanup(() => controller.abort())
  })

  createEffect(() => {
    const controller = new AbortController()
    void (async () => {
      try {
        for await (const update of props.gateway.followContextPressure(controller.signal)) {
          setContextPressures(current => update.seq > (current[update.sessionId]?.seq ?? -1)
            ? { ...current, [update.sessionId]: { pressure: update.pressure, seq: update.seq } }
            : current)
        }
      } catch (cause) { if (!controller.signal.aborted) setError(`Context meter disconnected: ${String(cause)}`) }
    })()
    onCleanup(() => controller.abort())
  })

  createEffect(() => {
    const controller = new AbortController()
    void (async () => {
      try {
        for await (const event of props.gateway.followApprovals(controller.signal)) {
          if (event.type === "request") { setApproval(event.request); setApprovalChoice(0); setApprovalPending(false) }
          else if (approval()?.eventId === event.eventId) { setApproval(undefined); setApprovalPending(false) }
        }
      } catch (cause) { if (!controller.signal.aborted) setError(`Approval stream disconnected: ${String(cause)}`) }
    })()
    onCleanup(() => controller.abort())
  })

  const answerApproval = async (decision: "allowed-once" | "rejected") => {
    const request = approval()
    if (!request || approvalPending()) return
    setApprovalPending(true)
    try { await props.gateway.answerApproval(request.clientId, request.eventId, decision) }
    catch (cause) { setApprovalPending(false); setError(`Failed to answer approval: ${String(cause)}`) }
  }

  const onInput = (value: string) => {
    setDraft(value)
    if (/^\/\S*$/.test(value)) {
      const matches = completeCommand(value, commandCandidates(remoteCommands(), commandFlags))
      setCommandOptions(matches); setSelected(0); setOverlay(matches.length ? "commands" : undefined)
      return
    }
    if (overlay() === "commands") setOverlay(undefined)
    const mention = findActiveMention(value, value.length)
    if (!mention) { if (overlay() === "references") setOverlay(undefined); return }
    const query = mention.query
    void props.gateway.completeFileReferences(sessionId(), query).then(items => {
      if (findActiveMention(draft(), draft().length)?.query !== query) return
      setReferences(rankReferences(items.map(item => ({ kind: item.kind, label: item.path, insertText: `@${item.path}` })), query).slice(0, 12))
      openOverlay("references")
    }).catch(() => {})
  }

  useBindings(() => ({
    commands: [
      { name: "model.list", run: () => openOverlay("models") },
      { name: "session.list", run: () => openOverlay("sessions") },
      { name: "session.new", run: () => void createSession() },
      { name: "session.page.up", run: () => transcript?.scrollBy(-transcript.height / 2) },
      { name: "session.page.down", run: () => transcript?.scrollBy(transcript.height / 2) },
      { name: "command.palette", run: () => { setCommandOptions(commandCandidates(remoteCommands(), commandFlags)); openOverlay("commands") } },
      { name: "app.quit", run: () => renderer.destroy() },
      ...(props.initial.agentTeams ? [{ name: "team.dag", run: () => void openTeamDag() }] : []),
    ],
    bindings: [
      { key: "<leader>m", cmd: "model.list" }, { key: "<leader>l", cmd: "session.list" },
      { key: "<leader>n", cmd: "session.new" }, { key: "<leader>q", cmd: "app.quit" },
      { key: "pageup", cmd: "session.page.up" }, { key: "pagedown", cmd: "session.page.down" },
      { key: "ctrl+p", cmd: "command.palette" },
      ...(props.initial.agentTeams ? [{ key: "<leader>t", cmd: "team.dag" }] : []),
    ],
  }))
  useKeyboard(key => {
    if (approval()) {
      if (key.name === "left" || key.name === "h") setApprovalChoice(0)
      if (key.name === "right" || key.name === "l") setApprovalChoice(1)
      if (key.name === "return") void answerApproval(approvalChoice() === 0 ? "allowed-once" : "rejected")
      if (key.name === "escape" || (key.ctrl && key.name === "c")) void answerApproval("rejected")
      return
    }
    if (key.name === "escape") { if (overlay()) setOverlay(undefined); else if (running()) void props.gateway.cancel(sessionId()) }
    if (key.ctrl && key.name === "c") { if (overlay()) setOverlay(undefined); else renderer.destroy() }
    if (overlay() && (key.name === "down" || (key.ctrl && key.name === "n"))) move(1)
    if (overlay() && (key.name === "up" || (key.ctrl && key.name === "p"))) move(-1)
    if (overlay() && (key.name === "return" || key.name === "tab")) {
      key.preventDefault()
      void acceptOverlay()
    }
  })

  return <box flexDirection="row" height="100%" backgroundColor={theme.bg}>
    <box flexGrow={1} flexDirection="column" paddingLeft={2} paddingRight={2}>
      <scrollbox ref={value => { transcript = value }} flexGrow={1} minHeight={1} stickyScroll stickyStart="bottom" scrollY verticalScrollbarOptions={{ visible: false }} flexDirection="column" paddingTop={1} paddingBottom={1}>
        <Show when={!nodes().length}><box height={12} justifyContent="center" alignItems="center" flexDirection="column"><text fg={theme.text}>DeepSeek Harness</text><text fg={theme.muted}>What can I help you build?</text></box></Show>
        <For each={nodes()}>{node => <Message node={node} syntax={syntax} />}</For>
      </scrollbox>
      <Show when={error()}>{message => <box paddingLeft={1} paddingRight={1} marginBottom={1} backgroundColor="#351c22"><text fg={theme.error}>{message()}</text></box>}</Show>
      <Show when={approval()} fallback={<Composer ref={value => { editor = value }} value={draft()} running={running()} syntax={syntax} history={promptHistory()} historyActive={!overlay()} maxHeight={Math.max(6, Math.floor(dimensions().height / 3))} onInput={onInput} onSubmit={value => void submit(value)} />}>
        {request => <PermissionPrompt request={request()} selected={approvalChoice()} pending={approvalPending()} tool={nodes().find(node => node.toolArgs && node.id === `tool:${request().callId}`)} />}
      </Show>
      <WorkingIndicator active={running() && !approval()} />
    </box>
    <Show when={dimensions().width >= 120}><Sidebar title={title()} cwd={props.cwd} model={activeModel()} tokens={tokens()} occupancy={occupancy()} mcp={props.initial.mcp} status={running() ? "working" : status()} /></Show>
    <Show when={overlay()}>{value => <Dialog overlay={value()} selected={selected()} sessions={sessions()} models={models()} reasoningEfforts={reasoningEfforts()} references={references()} commands={commandOptions()} teamLines={teamLines()} hasAgentTeams={props.initial.agentTeams} />}</Show>
  </box>
}

function WorkingIndicator(props: { active: boolean }) {
  const frames = [".  ", ".. ", "...", " ..", "  ."]
  const [frame, setFrame] = createSignal(0)
  const timer = setInterval(() => setFrame(value => (value + 1) % frames.length), 180)
  onCleanup(() => clearInterval(timer))
  return <box height={1} paddingLeft={2}><text fg={theme.muted}>{props.active ? frames[frame()] : ""}</text></box>
}

function PermissionPrompt(props: { request: ApprovalRequest; selected: number; pending: boolean; tool?: ConversationNode }) {
  const detail = () => props.tool?.toolName === "bash" ? `$ ${String(props.tool.toolArgs?.command ?? "")}` : props.tool?.toolArgs ? JSON.stringify(props.tool.toolArgs, null, 2) : ""
  return <box maxHeight={15} flexShrink={0} border={["left"]} borderColor={theme.warn} backgroundColor={theme.panel} flexDirection="column">
    <box paddingLeft={1} paddingRight={2} paddingTop={1} paddingBottom={1} flexDirection="column">
      <text fg={theme.warn}>△ Permission required</text>
      <text fg={theme.text}>{props.request.reason ?? `Tool ${props.request.toolName} requests privileged execution`}</text>
      <Show when={detail()}><box marginTop={1} backgroundColor={theme.panelActive} paddingLeft={1} paddingRight={1}><text fg={theme.text}>{detail()}</text></box></Show>
    </box>
    <box backgroundColor={theme.panelActive} paddingLeft={1} paddingRight={1} gap={1}>
      <box backgroundColor={props.selected === 0 ? theme.warn : theme.panel} paddingLeft={1} paddingRight={1}><text fg={props.selected === 0 ? theme.bg : theme.muted}>Allow once</text></box>
      <box backgroundColor={props.selected === 1 ? theme.error : theme.panel} paddingLeft={1} paddingRight={1}><text fg={props.selected === 1 ? theme.bg : theme.muted}>Reject</text></box>
      <text fg={theme.muted}>{props.pending ? "answering..." : "←→ select  enter confirm  esc reject"}</text>
    </box>
  </box>
}

function Message(props: { node: ConversationNode; syntax: SyntaxStyle }) {
  return props.node.kind === "user"
    ? <box backgroundColor={theme.panel} border={["left"]} borderColor={theme.accent} paddingLeft={1} paddingRight={1} paddingTop={1} paddingBottom={1} marginBottom={1}><text fg={theme.text}>{props.node.text}</text></box>
    : props.node.kind === "tool"
      ? <ToolCard node={props.node} syntax={props.syntax} />
      : <box flexDirection="column" marginBottom={1}><markdown content={props.node.text.trim()} syntaxStyle={props.syntax} renderNode={renderMarkdownNode} streaming={!props.node.complete} internalBlockMode="top-level" tableOptions={{ style: "grid" }} conceal fg={theme.text} bg={theme.bg} /></box>
}

function filetype(path: string): string { return path.split(".").at(-1) ?? "text" }

function ExpandRow(props: { hidden: number; expanded: boolean; onToggle(): void }) {
  const label = () => props.expanded
    ? "▾ click to collapse"
    : `▸ click here to expand (${props.hidden} more line${props.hidden === 1 ? "" : "s"})`
  return <box onMouseDown={event => { event.preventDefault(); props.onToggle() }}><text fg={theme.accent}>{label()}</text></box>
}

function ToolCard(props: { node: ConversationNode; syntax: SyntaxStyle }) {
  const [expanded, setExpanded] = createSignal(false)
  const [showCommand, setShowCommand] = createSignal(false)
  const args = () => props.node.toolArgs ?? {}
  const name = () => props.node.toolName ?? "tool"
  const path = () => String(args().file_path ?? args().path ?? "")
  const command = () => String(args().command ?? "")
  const source = () => String(args().content ?? args().new_string ?? "")
  const isShell = () => name() === "bash"
  const summary = () => isShell() ? "" : path() || String(args().description ?? "")
  const clippedOutput = () => clipText(props.node.toolOutput ?? "")
  const clippedSource = () => clipText(source())
  const outputText = () => expanded() ? props.node.toolOutput ?? "" : clippedOutput().visible
  const sourceText = () => expanded() ? source() : clippedSource().visible
  const toggle = () => setExpanded(value => !value)
  const status = () => props.node.complete ? "✓" : "●"
  const statusColor = () => props.node.isError ? theme.error : theme.muted
  return <box flexDirection="column" marginBottom={1} paddingLeft={1}>
    <Show when={isShell()} fallback={<text fg={statusColor()}>{status()} {name()}  {summary()}</text>}>
      <box flexDirection="row" onMouseDown={event => { event.preventDefault(); setShowCommand(value => !value) }}>
        <text fg={statusColor()}>{status()} </text>
        <text fg={theme.accent}>❯ Shell</text>
      </box>
    </Show>
    <Show when={isShell() && showCommand() && command()}>
      <box backgroundColor={theme.panel} paddingLeft={1} paddingRight={1} paddingTop={1} paddingBottom={1} marginTop={1}>
        <code content={command()} filetype="bash" syntaxStyle={props.syntax} conceal={false} wrapMode="word" drawUnstyledText onHighlight={shellFallback} />
      </box>
    </Show>
    <Show when={isShell() && props.node.toolOutput}>
      <box backgroundColor={theme.panel} paddingLeft={1} paddingRight={1} marginTop={1} flexDirection="column">
        <text fg={props.node.isError ? theme.error : theme.text}>{outputText()}</text>
        <Show when={clippedOutput().hidden}><ExpandRow hidden={clippedOutput().hidden} expanded={expanded()} onToggle={toggle} /></Show>
      </box>
    </Show>
    <Show when={(name() === "write" || name() === "edit") && source()}>
      <box backgroundColor={theme.panel} paddingLeft={1} paddingRight={1} paddingTop={1} paddingBottom={1} marginTop={1} flexDirection="column">
        <text fg={theme.muted}>{path()}</text>
        <code content={sourceText()} filetype={pathToFiletype(path()) ?? filetype(path())} syntaxStyle={props.syntax} conceal={false} wrapMode="word" fg={theme.text} />
        <Show when={clippedSource().hidden}><ExpandRow hidden={clippedSource().hidden} expanded={expanded()} onToggle={toggle} /></Show>
      </box>
    </Show>
    <Show when={!isShell() && name() !== "write" && name() !== "edit" && props.node.toolOutput}>
      <box backgroundColor={theme.panel} paddingLeft={1} paddingRight={1} marginTop={1} flexDirection="column">
        <text fg={props.node.isError ? theme.error : theme.muted}>{outputText()}</text>
        <Show when={clippedOutput().hidden}><ExpandRow hidden={clippedOutput().hidden} expanded={expanded()} onToggle={toggle} /></Show>
      </box>
    </Show>
  </box>
}

function Composer(props: { ref(value: TextareaRenderable): void; value: string; running: boolean; syntax: SyntaxStyle; history: string[]; historyActive: boolean; maxHeight: number; onInput(value: string): void; onSubmit(value: string): void }) {
  let textarea: TextareaRenderable | undefined
  let historyIndex = -1
  let draftBeforeHistory = ""
  const trackedPastes = (): TrackedPaste[] => textarea?.extmarks.getAll().flatMap(mark => {
    const metadata = textarea?.extmarks.getMetadataFor(mark.id) as { text?: string } | undefined
    return metadata?.text ? [{ start: mark.start, end: mark.end, text: metadata.text }] : []
  }) ?? []
  const onPaste = (event: PasteEvent) => {
    if (!textarea) return
    const text = new TextDecoder().decode(event.bytes).replace(/\r\n/g, "\n").replace(/\r/g, "\n")
    const summary = pasteSummary(text)
    if (!summary) return
    event.preventDefault()
    const start = textarea.cursorOffset
    textarea.insertText(`${summary.label} `)
    textarea.extmarks.create({ start, end: start + summary.label.length, styleId: props.syntax.getStyleId("paste") ?? undefined, typeId: textarea.extmarks.registerType("paste"), metadata: { text: summary.text } })
  }
  const applyHistory = (text: string) => {
    if (!textarea) return
    textarea.setText(text)
    textarea.cursorOffset = text.length
    props.onInput(text)
  }
  const onHistoryKey = (event: KeyEvent) => {
    if (!props.historyActive || !textarea || (event.name !== "up" && event.name !== "down")) return
    const text = textarea.plainText
    const offset = textarea.cursorOffset
    if (event.name === "up") {
      if (text.slice(0, offset).includes("\n") || !props.history.length) return
      if (historyIndex === -1) {
        draftBeforeHistory = text
        historyIndex = props.history.length - 1
      } else if (historyIndex > 0) historyIndex--
      applyHistory(props.history[historyIndex] ?? "")
      event.preventDefault()
      return
    }
    if (text.slice(offset).includes("\n") || historyIndex === -1) return
    if (historyIndex >= props.history.length - 1) {
      historyIndex = -1
      applyHistory(draftBeforeHistory)
    } else {
      historyIndex++
      applyHistory(props.history[historyIndex] ?? "")
    }
    event.preventDefault()
  }
  return <box minHeight={3} flexShrink={0} backgroundColor={theme.panel} border={["left"]} borderColor={theme.accent} paddingLeft={2} paddingRight={2} paddingTop={1} paddingBottom={1}>
    <textarea ref={value => { textarea = value; props.ref(value) }} initialValue={props.value} width="100%" minHeight={1} maxHeight={props.maxHeight} wrapMode="word" syntaxStyle={props.syntax} textColor={theme.text} focusedTextColor={theme.text} placeholderColor={theme.muted} backgroundColor={theme.panel} focusedBackgroundColor={theme.panel} onPaste={onPaste} onKeyDown={onHistoryKey} onContentChange={() => props.onInput(textarea?.plainText ?? "")} onSubmit={() => { historyIndex = -1; props.onSubmit(expandTrackedPastes(textarea?.plainText ?? "", trackedPastes())) }} keyBindings={[{ name: "return", action: "submit" }, { name: "j", ctrl: true, action: "newline" }, { name: "return", shift: true, action: "newline" }, { name: "return", ctrl: true, action: "newline" }, { name: "return", meta: true, action: "newline" }]} placeholder={props.running ? "Agent is working; press Esc to interrupt" : "Ask anything..."} focused />
  </box>
}

function mcpColor(server: McpStatus): string {
  if (!server.enabled || server.phase === null) return theme.muted
  if (server.phase === "active") return theme.good
  if (server.phase === "failed") return theme.error
  return theme.warn
}

function Sidebar(props: { title: string; cwd: string; model: ModelSelection; tokens: number; occupancy?: { percent: number; usedTokens: number; contextWindow: number }; mcp: readonly McpStatus[]; status: string }) {
  return <box width={42} backgroundColor={theme.sidebar} padding={2} flexDirection="column">
    <box alignItems="flex-end" flexDirection="column"><text fg="#8b5cf6">{dshLogo.slice(0, 3).join("\n")}</text><text fg={theme.accent}>{dshLogo.slice(3).join("\n")}</text></box>
    <text> </text><text fg={theme.text}>{props.title}</text><text> </text>
    <text fg={theme.text}>Context</text><text fg={theme.muted}>{props.occupancy ? `${props.occupancy.usedTokens.toLocaleString()} / ${props.occupancy.contextWindow.toLocaleString()} tokens (${props.occupancy.percent}%)` : `${props.tokens.toLocaleString()} tokens`}</text><text> </text>
    <text fg={theme.text}>Model</text><text fg={theme.muted}>{modelLabel(props.model)}</text><text> </text>
    <text fg={theme.text}>Workspace</text><text fg={theme.muted}>{props.cwd}</text>
    <Show when={props.mcp.length}><text> </text><text fg={theme.text}>MCP</text><For each={props.mcp}>{server => <text fg={mcpColor(server)}>● {server.name}</text>}</For></Show>
    <box flexGrow={1} />
    <text fg={theme.good}>● {props.status}</text>
  </box>
}

function Dialog(props: { overlay: Exclude<Overlay, undefined>; selected: number; sessions: readonly SessionSummary[]; models: readonly ModelOption[]; reasoningEfforts: readonly ReasoningEffortOption[]; references: readonly ReferenceCandidate[]; commands: readonly CommandCandidate[]; teamLines: readonly string[]; hasAgentTeams: boolean }) {
  const title = () => props.overlay === "sessions" ? "Sessions" : props.overlay === "models" ? "Select model" : props.overlay === "reasoning" ? "Select reasoning effort" : props.overlay === "references" ? "References" : props.overlay === "commands" ? "Commands" : props.overlay === "team" ? "Agent team DAG" : "Help"
  const items = () => props.overlay === "sessions" ? props.sessions.map(item => `${item.running ? "●" : " "} ${sessionLabel(item)}   ${item.sessionId}`)
    : props.overlay === "models" ? props.models.map(item => `${item.provider}/${item.model}   ${item.name}`)
    : props.overlay === "reasoning" ? props.reasoningEfforts.map(item => `${item.name}${item.description ? `   ${item.description}` : ""}`)
    : props.overlay === "references" ? props.references.map(item => `${item.kind === "directory" ? "DIR " : "FILE"}  ${item.label}`)
    : props.overlay === "commands" ? props.commands.map(commandLabel)
    : props.overlay === "team" ? props.teamLines
    : ["Ctrl+X M  models", "Ctrl+X L  sessions", "Ctrl+X N  new session", "Ctrl+P    command palette", ...(props.hasAgentTeams ? ["Ctrl+X T  team DAG"] : []), "↑↓        prompt history", "Esc       close / interrupt", "Click     expand tool output / Shell command"]
  const limit = () => props.overlay === "team" ? 40 : 14
  return <box position="absolute" left="18%" top="16%" width="64%" zIndex={3000} backgroundColor={theme.panel} border borderColor="#444444" flexDirection="column" padding={1}><text fg={theme.text}>{title()}</text><text fg={theme.muted}>────────────────────────────────────────</text><For each={items().slice(0, limit())}>{(item, index) => <box backgroundColor={index() === props.selected ? theme.panelActive : theme.panel} paddingLeft={1} paddingRight={1}><text fg={index() === props.selected ? theme.text : theme.muted}>{item}</text></box>}</For><text fg={theme.muted}>{props.overlay === "team" ? "↑↓ scroll  enter refresh  esc close" : "↑↓ navigate  enter select  esc close"}</text></box>
}

export async function startApp(props: AppProps): Promise<void> {
  await render(() => <KeymapRoot {...props} />, { exitOnCtrlC: false, openConsoleOnError: true, backgroundColor: theme.bg })
}
