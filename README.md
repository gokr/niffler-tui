# niffler-tui

[English](README.md) · [简体中文](README.zh.md) · [繁體中文](README.zh-TW.md) · [Discord](https://discord.gg/ThJFEAJUAk)

The terminal chat client for [Niffler](https://github.com/gokr/niffler), a
self-extending coding agent harness. Talk to the agent, watch its tools work,
and pick up conversations where you left off—all from your terminal.

- Streaming Markdown replies, reasoning and collapsible tool cards.
- Conversation history, searchable sessions and provider/model pickers.
- File attachments, clipboard selection and approval prompts.
- Themes and English, Simplified Chinese and Traditional Chinese interfaces.

## Get started

From your **Niffler harness checkout**, install the client and its launcher:

```bash
make install WITH_TUI=1
```

Configure a model provider in Niffler, then open a real terminal in the project
where you want the agent to work:

```bash
cd /path/to/your/project
niffler-tui
```

The installed launcher starts the local harness when needed. Type a request and
press `Enter`. The current directory becomes the new conversation's workspace;
a saved conversation for this harness/workspace is resumed when available.
Use `/new` for a fresh conversation or `/session` to browse previous ones.

No provider yet? Use `/connect` to enter a connection, or `/provider` to choose
an existing provider or subscription login. `/model` selects a model for the
current conversation. Credentials entered in the connection form are masked
and kept out of chat history.

## Everyday controls

| Key / command | Action |
|---|---|
| `Enter` | Send; while busy, steer the running turn |
| `Alt+Enter` / `Ctrl+J` | Insert a newline |
| `Tab` | Complete commands and arguments |
| `Up` / `Down`, `Ctrl+R` | Recall or search input history |
| `PgUp` / `PgDn` | Scroll the conversation |
| `Ctrl+T` / `Ctrl+E` | Adjust reasoning / tool-card detail |
| `Esc`, then `Esc` | Confirm stopping a running turn |
| `/new`, `/session` | Start or switch conversations |
| `/theme`, `/locale` | Change appearance or language |
| `/help` | All built-in and plugin commands |
| `Ctrl+C` | Quit the client |

Mouse wheel scrolls; drag to select and copy text. Approval prompts ask before
protected agent actions. Quitting the client does not stop a running agent turn.

## Learn more

The **[Manual](MANUAL.md)** contains the full key and command reference,
configuration, shell escapes, client-state migration, development instructions
and experimental multi-instance/fleet support.

Client-local preferences and state live under `~/.niffler/` on Linux and macOS;
conversation transcripts live in the harness store. See
[client state](MANUAL.md#transcript-and-client-state) for details.

## Build from source

With the matching Niffler checkout available through the local Go SDK replacement:

```bash
make build      # bin/niffler-tui
make test
```

Start the harness first, then run `./bin/niffler-tui` from a terminal. Building
the binary alone does not install the autostart/restart launcher. See
[Build](MANUAL.md#build) and [plugin installation](MANUAL.md#plugin-status).
