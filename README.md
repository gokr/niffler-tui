# niffler-tui

A Bubble Tea terminal client for Niffler. It is a bus client like the desktop
UI: it drives `session` calls through `svc.core.call` and renders
`ev.session.*` token, tool-call, assistant, and completion events.

The component registers with zero tools. Running it does not add anything to
an LLM's tool context.

## Build

This checkout is expected next to Niffler so the local `go.mod` replacement
finds the Go SDK:

```bash
go build -o bin/niffler-tui ./tui
```

The Niffler builder does not use this `go.mod`; it creates an isolated module
with a replacement pointing at its own `sdk/go` directory.

## Run

Start Niffler, then run the client from a real terminal:

```bash
./bin/niffler-tui
./bin/niffler-tui -session game
```

Configuration:

- `-session <id>` selects the conversation, defaulting to `NIF_SESSION` and
  then `console`.
- `NIF_NATS_URL` selects the bus. When unset, the client checks
  `$NIF_ROOT/var/nats-url`, then `./var/nats-url`, then
  `nats://127.0.0.1:4222`.

Keys:

- `Enter`: send the current input
- `Alt+Enter` / `Ctrl+J`: insert a newline in the input (multiline messages)
- `Up` / `Down`: move within the input; at the first/last line they walk
  sent-message history (readline-style), restoring the in-progress draft
- `Ctrl+R`: reverse history search; type to filter, `Ctrl+R` again for older
  matches, `Enter` to accept, `Esc` to cancel
- `PgUp` / `PgDn`: page through the transcript
- `Ctrl+Up` / `Ctrl+Down`: scroll the transcript one line at a time
- Mouse wheel: scroll the transcript
- `Ctrl+C`: quit

Local commands are handled by the TUI and are never sent to the model:

- `/provider` — searchable global provider selector; also offers the
  environment fallback and provider setup
- `/model` — searchable active-provider model catalog; selection is persisted
  immediately as this conversation's override (without an inference call)
- `/connect` — masked provider connection form using models.dev templates or a
  custom OpenAI-compatible endpoint
- `/status` — detailed effective provider/model/context provenance and usage
- `/help` — command summary

Selectors use `/` to filter, arrows to move, `Enter` to choose, and `Esc` to
return to chat. The provider form masks its API-key field; slash commands and
credentials are not written to sent-message history or the transcript.

The second header line always shows the effective provider and model plus a
context gauge. Context occupancy uses model-reported total tokens when
available, survives session resume through Niffler's conversation metadata,
and changes colour at the same 75% warning / 90% trimming thresholds as core.
Provider selection changes Niffler's global default; model selection is scoped
to the current conversation.

Assistant replies are rendered as Markdown (headings, bold, italics, lists,
tables, and syntax-highlighted fenced code blocks via Glamour). While tokens
stream, blocks show as plain text and upgrade to styled Markdown when output
pauses, avoiding an expensive full Markdown render on every token.

History is persisted per session as JSONL under
`$XDG_STATE_HOME/niffler-tui/history-<session>.jsonl` (or
`~/.local/state/niffler-tui/`), capped at 200 entries. Delete the file to
clear it.

The Markdown style follows the `GLAMOUR_STYLE` environment variable (default
`dark`; see Glamour's styles, e.g. `light`, `ascii`, `dracula`,
`tokyo-night`).

The alternate screen keeps the viewport and input stable while tokens stream.
When the transcript is scrolled up, new tokens do not force it back to the
bottom.

## Plugin status

`niffler.json` marks the component as `"interactive": true`: it should be
built by plugin installation but started manually from the user's terminal:

```bash
./var/bin/cli install gokr/niffler-tui
./var/bin/tui -session game
```

Interactive components are not supervised or restarted by Niffler. Quit any
running TUI before updating or removing the package.
