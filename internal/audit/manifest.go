package audit

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"guji-paper/internal/domain"
)

func (m Manifest) Sealed() bool { return m.Verified && m.ManifestSHA256 != "" }

type ManifestVerification struct {
	Verified            bool   `json:"verified"`
	ManifestHashMatch   bool   `json:"manifest_hash_match"`
	EntriesMatch        bool   `json:"entries_match"`
	EventChainRootMatch bool   `json:"event_chain_root_match"`
	CaseRevisionMatch   bool   `json:"case_revision_match"`
	StoredVerified      bool   `json:"stored_verified"`
	RecomputedSHA256    string `json:"recomputed_sha256"`
	Error               string `json:"error,omitempty"`
}

func VerifyManifest(m Manifest, events []domain.Event, caseRevision int) ManifestVerification {
	raw, _ := json.Marshal(m.Entries)
	h := sha256.Sum256(raw)
	recomputed := hex.EncodeToString(h[:])
	root := ""
	if len(events) > 0 {
		root = events[len(events)-1].Hash
	}
	report := ManifestVerification{
		ManifestHashMatch:   recomputed == m.ManifestSHA256,
		EntriesMatch:        manifestEntriesMatch(m.Entries, events),
		EventChainRootMatch: root == m.EventChainRoot,
		CaseRevisionMatch:   caseRevision == m.CaseRevision && len(events) == m.CaseRevision,
		StoredVerified:      m.Verified,
		RecomputedSHA256:    recomputed,
	}
	report.Verified = report.ManifestHashMatch && report.EntriesMatch && report.EventChainRootMatch && report.CaseRevisionMatch && report.StoredVerified && VerifyEventRange(events, 1, 0).Verified
	if !report.Verified {
		report.Error = "manifest_verification_failed"
	}
	return report
}

func manifestEntriesMatch(entries []string, events []domain.Event) bool {
	if len(entries) != len(events) {
		return false
	}
	for i, entry := range entries {
		var stored any
		if json.Unmarshal([]byte(entry), &stored) != nil {
			return false
		}
		storedBytes, _ := json.Marshal(stored)
		eventBytes, _ := json.Marshal(events[i])
		var current any
		if json.Unmarshal(eventBytes, &current) != nil {
			return false
		}
		currentBytes, _ := json.Marshal(current)
		if string(storedBytes) != string(currentBytes) {
			return false
		}
	}
	return true
}
