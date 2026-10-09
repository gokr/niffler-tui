package main

import (
	"sort"

	sdk "niffler.dev/sdk"
)

// Clean verdicts are separate store documents, not model messages. Actionable
// verdicts already have transcript messages, so never replay those twice.
func mergeCleanDiagnostics(comp *sdk.Component, session string, messages []storedMessage) []storedMessage {
	var after string
	var added bool
	var earliest float64
	if len(messages) > 0 {
		earliest = messages[0].CreatedAt
	}
	for {
		var page struct {
			Items []struct {
				ID    string `json:"id"`
				Value struct {
					Path      string  `json:"path"`
					Text      string  `json:"text"`
					Clean     bool    `json:"clean"`
					CreatedAt float64 `json:"createdAt"`
				} `json:"value"`
			} `json:"items"`
			HasMore   bool   `json:"hasMore"`
			NextAfter string `json:"nextAfter"`
		}
		if err := requestInto(comp, "store", "list", map[string]any{
			"kind": "diagnostic", "idPrefix": session + ":", "limit": historyPageSize, "after": after,
		}, &page); err != nil {
			break
		}
		for _, item := range page.Items {
			v := item.Value
			if v.Clean && v.CreatedAt >= earliest {
				messages = append(messages, storedMessage{Role: "diagnostic", Name: v.Path, Content: messageContent(v.Text), CreatedAt: v.CreatedAt})
				added = true
			}
		}
		if !page.HasMore || page.NextAfter == "" || page.NextAfter == after {
			break
		}
		after = page.NextAfter
	}
	if added {
		sort.SliceStable(messages, func(i, j int) bool { return messages[i].CreatedAt < messages[j].CreatedAt })
	}
	return messages
}
