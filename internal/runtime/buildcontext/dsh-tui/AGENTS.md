# dsh-tui Rules

- DSH is authoritative. Never read or write its session persistence files.
- Keep all transport and Remote protocol knowledge in `src/dsh/`.
- Do not add static model lists, local tool execution, or a second subagent runtime.
- Use DSH's session-aware reference APIs for `@` completion.
