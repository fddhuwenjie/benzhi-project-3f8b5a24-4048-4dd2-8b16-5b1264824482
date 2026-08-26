package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

func (c *Case) EventCount() int { return len(c.Events) }

func ComputeEventHash(e Event) string {
	dataBytes, _ := json.Marshal(e.Data)
	var canonicalData any
	_ = json.Unmarshal(dataBytes, &canonicalData)
	payload, _ := json.Marshal(struct {
		Revision  int    `json:"revision"`
		Type      string `json:"type"`
		Data      any    `json:"data"`
		RequestID string `json:"request_id"`
		PrevHash  string `json:"prev_hash"`
	}{e.Revision, e.Type, canonicalData, e.RequestID, e.PrevHash})
	h := sha256.Sum256(payload)
	return hex.EncodeToString(h[:])
}
