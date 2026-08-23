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
- `Page Up` / `Page Down` or mouse wheel: scroll the transcript
- `Ctrl+C`: quit

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
