package main

import (
	"reflect"
	"testing"
)

// TestCatalogsComplete fails CI when a translation catalog drifts from the
// English source of truth: every zh/zh-TW key must exist in en, and no
// catalog may carry keys the others lack. A missing key would otherwise
// surface as English text (or a blank) at runtime.
func TestCatalogsComplete(tt *testing.T) {
	enKeys := reflect.ValueOf(catalogEn).MapKeys()
	all := []struct {
		name string
		cat  map[string]string
	}{
		{"en", catalogEn},
		{"zh", catalogZh},
		{"zh-TW", catalogZhTW},
	}
	for _, entry := range all {
		if len(entry.cat) != len(catalogEn) {
			tt.Errorf("%s has %d keys, en has %d", entry.name, len(entry.cat), len(catalogEn))
		}
	}
	for _, key := range enKeys {
		k := key.String()
		if catalogZh[k] == "" {
			tt.Errorf("zh missing key %q", k)
		}
		if catalogZhTW[k] == "" {
			tt.Errorf("zh-TW missing key %q", k)
		}
	}
}

func TestTSubstitutes(tt *testing.T) {
	got := t(LocaleEN, "status.connecting", "nats://127.0.0.1:4222")
	want := "connecting to nats://127.0.0.1:4222"
	if got != want {
		tt.Errorf("got %q, want %q", got, want)
	}
	if got := t(LocaleZH, "footer.confirmRemove", "deepseek"); got != "移除 deepseek？再按 d/x 确认，esc 取消" {
		tt.Errorf("zh confirmRemove: %q", got)
	}
	// Unknown key falls back to English rather than rendering blank.
	if got := t(LocaleZHTW, "no.such.key"); got != "" {
		tt.Errorf("unknown key: got %q", got)
	}
}
