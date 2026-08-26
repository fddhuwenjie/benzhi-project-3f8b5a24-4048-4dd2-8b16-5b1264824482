package audit

import (
	"guji-paper/internal/domain"
)

type Report struct {
	Verified             bool   `json:"verified"`
	FirstInvalidRevision int    `json:"first_invalid_revision,omitempty"`
	Error                string `json:"error,omitempty"`
	RangeFirstPrevHash   string `json:"range_first_prev_hash,omitempty"`
	RangeFirstHash       string `json:"range_first_hash,omitempty"`
	RangeLastHash        string `json:"range_last_hash,omitempty"`
}

func Verify(c *domain.Case) bool {
	if c == nil || len(c.Events) == 0 {
		return false
	}
	for i, e := range c.Events {
		if e.Revision != i+1 || (i > 0 && e.PrevHash != c.Events[i-1].Hash) || e.Hash != domain.ComputeEventHash(e) {
			return false
		}
	}
	return true
}

func VerifyRange(c *domain.Case, from, to int) Report {
	if c == nil {
		return Report{Error: "case_not_found"}
	}
	return VerifyEventRange(c.Events, from, to)
}

func VerifyEventRange(events []domain.Event, from, to int) Report {
	if from < 1 {
		from = 1
	}
	if to <= 0 || to > len(events) {
		to = len(events)
	}
	report := Report{Verified: true}
	for i, e := range events {
		if e.Revision < from || e.Revision > to {
			continue
		}
		if report.RangeFirstHash == "" {
			report.RangeFirstPrevHash = e.PrevHash
			report.RangeFirstHash = e.Hash
		}
		report.RangeLastHash = e.Hash
		if e.Revision != i+1 || (i > 0 && e.PrevHash != events[i-1].Hash) || e.Hash != domain.ComputeEventHash(e) {
			return Report{Verified: false, FirstInvalidRevision: e.Revision, Error: "hash_chain_invalid"}
		}
	}
	return report
}
