// Clipboard writes for drag selection and /copy.
//
// OSC 52 is the primary path: it travels through the terminal, so it also
// reaches the human's clipboard over SSH and inside tmux when they permit
// it. Not every terminal implements it, though — Apple's Terminal.app
// silently drops the sequence, as does GNOME Terminal (VTE) — and with mouse
// tracking on, such a terminal has no native selection of its own for Cmd-C
// to copy either. When the client runs on the machine whose clipboard the
// human is looking at (no SSH session), the text therefore also goes through
// the platform's clipboard tool.
package main

import (
	"os"
	"os/exec"
	"runtime"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// copyToClipboard sets both the regular and primary clipboards over OSC 52
// and, for a local session, the platform clipboard as well.
func copyToClipboard(text string) tea.Cmd {
	cmds := []tea.Cmd{tea.SetClipboard(text), tea.SetPrimaryClipboard(text)}
	if argv := nativeClipboardArgv(runtime.GOOS, os.Getenv, exec.LookPath); argv != nil {
		cmds = append(cmds, nativeClipboardCmd(argv, text))
	}
	return tea.Batch(cmds...)
}

// nativeClipboardArgv picks the platform clipboard writer, or nil when there
// is none to use: over SSH the local tool would fill the remote machine's
// clipboard, so OSC 52 stays the only path there.
func nativeClipboardArgv(goos string, getenv func(string) string, lookPath func(string) (string, error)) []string {
	for _, key := range []string{"SSH_CONNECTION", "SSH_CLIENT", "SSH_TTY"} {
		if getenv(key) != "" {
			return nil
		}
	}
	var candidates [][]string
	switch goos {
	case "darwin":
		candidates = [][]string{{"pbcopy"}}
	case "linux", "freebsd", "openbsd", "netbsd":
		if getenv("WAYLAND_DISPLAY") != "" {
			candidates = append(candidates, []string{"wl-copy"})
		}
		if getenv("DISPLAY") != "" {
			candidates = append(candidates,
				[]string{"xclip", "-selection", "clipboard"},
				[]string{"xsel", "--clipboard", "--input"})
		}
	}
	for _, argv := range candidates {
		if _, err := lookPath(argv[0]); err == nil {
			return argv
		}
	}
	return nil
}

// nativeClipboardCmd feeds text to the clipboard tool off the UI loop. A
// failure stays silent: OSC 52 may well have succeeded, and the tool's
// output must never reach the alternate screen.
func nativeClipboardCmd(argv []string, text string) tea.Cmd {
	return func() tea.Msg {
		cmd := exec.Command(argv[0], argv[1:]...)
		cmd.Stdin = strings.NewReader(text)
		// pbcopy decodes its input by locale and falls back to the C
		// encoding, which would mangle non-ASCII text; Go strings are UTF-8.
		cmd.Env = append(os.Environ(), "LC_ALL=en_US.UTF-8")
		_ = cmd.Run()
		return nil
	}
}
