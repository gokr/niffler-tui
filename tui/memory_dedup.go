package main

import "encoding/json"

// Stable activity IDs join live and replayed memory without collapsing
// independent operations that happen to contain identical text.
func dedupMemoryBlocks(blocks []transcriptBlock) []transcriptBlock {
	seen := map[string]bool{}
	out := make([]transcriptBlock, 0, len(blocks))
	for _, b := range blocks {
		if b.kind == blockMemory {
			var a memoryActivity
			if json.Unmarshal([]byte(b.text), &a) == nil && a.ID != "" {
				if seen[a.ID] {
					continue
				}
				seen[a.ID] = true
			}
		}
		out = append(out, b)
	}
	return out
}
