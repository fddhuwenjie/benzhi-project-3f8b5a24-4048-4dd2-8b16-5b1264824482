package audit

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"guji-paper/internal/domain"
)

type EvidenceFilter struct {
	Stage        string
	FromRevision int
	ToRevision   int
}

type EvidenceItem struct {
	Revision    int    `json:"revision"`
	EventType   string `json:"event_type"`
	Stage       string `json:"evidence_stage"`
	EntrySHA256 string `json:"entry_sha256"`
}

type EvidenceSelection struct {
	Items                []EvidenceItem `json:"items"`
	Total                int            `json:"total"`
	SelectionSHA256      string         `json:"selection_sha256"`
	ManifestSHA256       string         `json:"manifest_sha256"`
	EventChainRoot       string         `json:"event_chain_root"`
	CaseRevision         int            `json:"case_revision"`
	ManifestVerified     bool           `json:"manifest_verified"`
	EventChainVerified   bool           `json:"event_chain_verified"`
	FirstInvalidRevision int            `json:"first_invalid_revision,omitempty"`
}

func SelectEvidence(m Manifest, events []domain.Event, caseRevision int, filter EvidenceFilter) (EvidenceSelection, error) {
	out := EvidenceSelection{Items: []EvidenceItem{}, ManifestSHA256: m.ManifestSHA256, EventChainRoot: m.EventChainRoot, CaseRevision: m.CaseRevision}
	chain := VerifyEventRange(events, 1, 0)
	out.EventChainVerified = chain.Verified
	verification := VerifyManifest(m, events, caseRevision)
	out.ManifestVerified = verification.Verified
	if !verification.Verified {
		out.FirstInvalidRevision = firstManifestMismatch(m, events, chain.FirstInvalidRevision)
		return out, errors.New("封存清单核验失败")
	}
	for _, entry := range m.Entries {
		var event domain.Event
		if json.Unmarshal([]byte(entry), &event) != nil {
			out.FirstInvalidRevision = event.Revision
			return out, errors.New("封存清单条目无效")
		}
		if event.Revision < filter.FromRevision || event.Revision > filter.ToRevision {
			continue
		}
		stage, ok := domain.EvidenceStageForEvent(event.Type)
		if !ok || stage != filter.Stage {
			continue
		}
		var normalized any
		_ = json.Unmarshal([]byte(entry), &normalized)
		canonical, _ := json.Marshal(normalized)
		hash := sha256.Sum256(canonical)
		out.Items = append(out.Items, EvidenceItem{Revision: event.Revision, EventType: event.Type, Stage: stage, EntrySHA256: hex.EncodeToString(hash[:])})
	}
	if len(out.Items) == 0 {
		return EvidenceSelection{}, errors.New("筛选条件未命中封存证据")
	}
	raw, _ := json.Marshal(out.Items)
	hash := sha256.Sum256(raw)
	out.SelectionSHA256 = hex.EncodeToString(hash[:])
	out.Total = len(out.Items)
	return out, nil
}

func firstManifestMismatch(m Manifest, events []domain.Event, chainRevision int) int {
	if chainRevision > 0 {
		return chainRevision
	}
	limit := len(m.Entries)
	if len(events) < limit {
		limit = len(events)
	}
	for i := 0; i < limit; i++ {
		var stored, current any
		if json.Unmarshal([]byte(m.Entries[i]), &stored) != nil {
			return i + 1
		}
		currentBytes, _ := json.Marshal(events[i])
		_ = json.Unmarshal(currentBytes, &current)
		left, _ := json.Marshal(stored)
		right, _ := json.Marshal(current)
		if string(left) != string(right) {
			return events[i].Revision
		}
	}
	if len(m.Entries) != len(events) {
		return limit + 1
	}
	return 0
}
