package main

import (
	"encoding/json"
	sdk "niffler.dev/sdk"
	"sort"
)

// Display-only operations live beside canonical messages. Failure to load the
// optional activity kind must never prevent transcript resume on old harnesses.
func mergeMemoryActivity(comp *sdk.Component, session string, messages []storedMessage) []storedMessage {
	var after string
	earliest := 0.0
	if len(messages) > 0 {
		earliest = messages[0].CreatedAt
	}
	for {
		var page struct {
			Items []struct {
				ID    string          `json:"id"`
				Value json.RawMessage `json:"value"`
			} `json:"items"`
			HasMore   bool   `json:"hasMore"`
			NextAfter string `json:"nextAfter"`
		}
		if requestInto(comp, "store", "list", map[string]any{"kind": "memory-activity", "idPrefix": session + ":", "after": after, "limit": historyPageSize}, &page) != nil {
			break
		}
		for _, item := range page.Items {
			var stamp struct {
				CreatedAt float64 `json:"createdAt"`
			}
			if json.Unmarshal(item.Value, &stamp) != nil || stamp.CreatedAt < earliest {
				continue
			}
			var activity memoryActivity
			if json.Unmarshal(item.Value, &activity) != nil {
				continue
			}
			if activity.ID == "" {
				activity.ID = item.ID
			}
			raw, _ := json.Marshal(activity)
			messages = append(messages, storedMessage{Role: "memory", Memory: raw, CreatedAt: stamp.CreatedAt})
		}
		if !page.HasMore || page.NextAfter == "" || page.NextAfter == after {
			break
		}
		after = page.NextAfter
	}
	sort.SliceStable(messages, func(i, j int) bool { return messages[i].CreatedAt < messages[j].CreatedAt })
	return messages
}
