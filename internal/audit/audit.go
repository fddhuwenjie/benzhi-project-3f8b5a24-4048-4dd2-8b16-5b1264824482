package audit

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"guji-paper/internal/domain"
)

type Manifest struct {
	ManifestID     string   `json:"manifest_id"`
	CaseID         string   `json:"case_id"`
	CaseRevision   int      `json:"case_revision"`
	Entries        []string `json:"entries,omitempty"`
	EventChainRoot string   `json:"event_chain_root"`
	ManifestSHA256 string   `json:"manifest_sha256"`
	Verified       bool     `json:"verified"`
	SealedBy       string   `json:"sealed_by"`
}

func Build(c *domain.Case, by string) Manifest {
	entries := make([]string, 0, len(c.Events))
	for _, e := range c.Events {
		b, _ := json.Marshal(e)
		entries = append(entries, string(b))
	}
	root := ""
	if len(c.Events) > 0 {
		root = c.Events[len(c.Events)-1].Hash
	}
	raw, _ := json.Marshal(entries)
	h := sha256.Sum256(raw)
	return Manifest{ManifestID: c.ID + "-manifest", CaseID: c.ID, CaseRevision: c.Revision, Entries: entries, EventChainRoot: root, ManifestSHA256: hex.EncodeToString(h[:]), Verified: true, SealedBy: by}
}
