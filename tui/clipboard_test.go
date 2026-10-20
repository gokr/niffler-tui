package main

import (
	"errors"
	"reflect"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// TestNativeClipboardArgv pins the fallback choice: the platform tool for a
// local session, nothing over SSH or without a usable display/tool.
func TestNativeClipboardArgv(t *testing.T) {
	cases := []struct {
		name      string
		goos      string
		env       map[string]string
		installed []string
		want      []string
	}{
		{"darwin local", "darwin", nil, []string{"pbcopy"}, []string{"pbcopy"}},
		{"darwin over ssh", "darwin", map[string]string{"SSH_CONNECTION": "1.2.3.4 5 6.7.8.9 22"}, []string{"pbcopy"}, nil},
		{"ssh tty only", "darwin", map[string]string{"SSH_TTY": "/dev/ttys001"}, []string{"pbcopy"}, nil},
		{"wayland", "linux", map[string]string{"WAYLAND_DISPLAY": "wayland-0", "DISPLAY": ":0"}, []string{"wl-copy", "xclip"}, []string{"wl-copy"}},
		{"wayland without wl-copy", "linux", map[string]string{"WAYLAND_DISPLAY": "wayland-0", "DISPLAY": ":0"}, []string{"xclip"}, []string{"xclip", "-selection", "clipboard"}},
		{"x11 xsel only", "linux", map[string]string{"DISPLAY": ":0"}, []string{"xsel"}, []string{"xsel", "--clipboard", "--input"}},
		{"linux console", "linux", nil, []string{"wl-copy", "xclip", "xsel"}, nil},
		{"linux no tool", "linux", map[string]string{"DISPLAY": ":0"}, nil, nil},
		{"windows", "windows", nil, []string{"clip"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			getenv := func(key string) string { return tc.env[key] }
			lookPath := func(bin string) (string, error) {
				for _, have := range tc.installed {
					if have == bin {
						return "/usr/bin/" + bin, nil
					}
				}
				return "", errors.New("not found")
			}
			if got := nativeClipboardArgv(tc.goos, getenv, lookPath); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// The fleet wrapper scopes every command to an instance and forwards only the
// terminal-effect messages it recognises. The native clipboard write returns
// nil and does its work as a side effect, so a wrapper that swallowed it would
// lose the copy in exactly the terminals this fallback exists for. Pin that the
// whole copyToClipboard batch survives scoping (both OSC 52 writes and the
// native writer).
func TestClipboardBatchSurvivesFleetScoping(t *testing.T) {
	called := false
	native := func() tea.Msg { called = true; return nil }
	scoped := scopeCmd("worker", 7, tea.Batch(
		tea.SetClipboard("x"), tea.SetPrimaryClipboard("x"), native))
	batch, ok := scoped().(tea.BatchMsg)
	if !ok {
		t.Fatalf("scoping swallowed the clipboard batch: %T", scoped())
	}
	for i := range batch {
		batch[i]()
	}
	if !called {
		t.Fatal("fleet scoping swallowed the native clipboard write")
	}
}
