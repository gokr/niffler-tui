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
make            # builds bin/niffler-tui
make test       # runs the TUI test suite
make run        # build + run
```

or directly:

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
- `Ctrl+T`: expand/collapse all tool-run cards
- Mouse wheel: scroll the transcript
- `Ctrl+C`: quit

Consecutive tool calls in a turn are folded into a single collapsible
"tool-run card" with a summary line (✓/⚠ glyph, call count, names), collapsed
by default so long tool sequences stay unobtrusive. `Ctrl+T` expands or
collapses every card. Mouse tracking is **on by default**: the wheel scrolls
the transcript and a left click toggles the card under the cursor. Because
tracking captures clicks, copy/paste selection uses `Shift`+drag (standard
for SGR mouse). `/mouse off` disables tracking entirely — native plain-drag
copy comes back, but the app then receives no wheel events, so the transcript
scrolls with `PgUp`/`PgDn`/`Ctrl+Up` and the wheel behaves as ordinary
terminal scroll instead.

Local commands are handled by the TUI and are never sent to the model:

- `/provider` — searchable global provider selector; also offers the
  environment fallback and provider setup
- `/model` — searchable active-provider model catalog; selection is persisted
  immediately as this conversation's override (without an inference call)
- `/connect` — masked provider connection form using models.dev templates or a
  custom OpenAI-compatible endpoint
- `/status` — detailed effective provider/model/context provenance and usage
- `/mouse [on|off]` — mouse tracking (default on: wheel+click work, copy is
  Shift+drag; off restores native plain-drag copy but no app wheel)
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
