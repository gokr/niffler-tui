package main

import (
	"errors"
	"reflect"
	"testing"
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
