package domain

import (
	"encoding/json"
	"sort"
)

// ReplayCaseFromEvents rebuilds an intermediate case snapshot from a prefix of
// the authoritative event chain. It applies each event's recorded data directly,
// without re-running validation, so the snapshot reflects the exact state at the
// moment the event at targetRevision completed.
func ReplayCaseFromEvents(events []Event) *Case {
	if len(events) == 0 {
		return nil
	}
	var c *Case
	for _, e := range events {
		if e.Type == "CASE_CREATED" {
			c = replayCreate(e)
		} else if c == nil {
			return nil
		}
		if err := replayApply(c, e); err != nil {
			return nil
		}
	}
	return c
}

func replayCreate(e Event) *Case {
	var p struct {
		ID         string `json:"case_id"`
		Batch      string `json:"batch"`
		Material   string `json:"material"`
		Purpose    string `json:"purpose"`
		Owner      string `json:"owner"`
		Reviewer   string `json:"reviewer"`
		Authorizer string `json:"authorizer"`
	}
	decodeEventData(e.Data, &p)
	return &Case{
		ID: p.ID, Batch: p.Batch, Material: p.Material, Purpose: p.Purpose,
		Owner: p.Owner, Reviewer: p.Reviewer, Authorizer: p.Authorizer,
		Status: Draft, Measurements: map[string]Measurement{},
		Discrepancies: map[string]Discrepancy{}, CreatedAt: e.At,
		Revision: e.Revision,
	}
}

func replayApply(c *Case, e Event) error {
	switch e.Type {
	case "CASE_CREATED":
		// initial state set in replayCreate
	case "PLAN_SUBMITTED":
		var p Plan
		decodeEventData(e.Data, &p)
		c.Plan = &p
		c.Status = PlanReady
	case "CONDITIONING_READING":
		var r ConditioningReading
		decodeEventData(e.Data, &r)
		c.Conditioning = append(c.Conditioning, r)
		sort.SliceStable(c.Conditioning, func(i, j int) bool { return c.Conditioning[i].At.Before(c.Conditioning[j].At) })
	case "CONDITIONING_BATCH_RECORDED":
		var payload struct {
			Readings []ConditioningReading `json:"readings"`
		}
		decodeEventData(e.Data, &payload)
		merged := make([]ConditioningReading, 0, len(c.Conditioning)+len(payload.Readings))
		merged = append(merged, c.Conditioning...)
		merged = append(merged, payload.Readings...)
		sort.SliceStable(merged, func(i, j int) bool { return merged[i].At.Before(merged[j].At) })
		c.Conditioning = merged
	case "CONDITIONED":
		c.Status = Conditioned
	case "MEASUREMENTS_RECORDED":
		var ms []Measurement
		decodeEventData(e.Data, &ms)
		if c.Measurements == nil {
			c.Measurements = map[string]Measurement{}
		}
		if c.Discrepancies == nil {
			c.Discrepancies = map[string]Discrepancy{}
		}
		for _, m := range ms {
			c.Measurements[m.Group] = m
		}
		recomputeDiscrepancies(c)
		c.Status = Tested
	case "REMEDIATION_MEASUREMENTS_RECORDED":
		var payload struct {
			Round        int           `json:"round"`
			Note         string        `json:"remediation_note"`
			Measurements []Measurement `json:"measurements"`
		}
		decodeEventData(e.Data, &payload)
		if c.Measurements == nil {
			c.Measurements = map[string]Measurement{}
		}
		if c.Discrepancies == nil {
			c.Discrepancies = map[string]Discrepancy{}
		}
		for _, m := range payload.Measurements {
			c.Measurements[m.Group] = m
		}
		c.Discrepancies = map[string]Discrepancy{}
		recomputeDiscrepancies(c)
		for i := range c.Remediations {
			if c.Remediations[i].Round == payload.Round {
				c.Remediations[i].ReplacementRevision = e.Revision
				c.Remediations[i].RemediationNote = payload.Note
				break
			}
		}
		c.Status = Tested
	case "DISCREPANCY_DECIDED":
		var d Discrepancy
		decodeEventData(e.Data, &d)
		d.HasRetest = len(d.Retest) > 0
		if c.Discrepancies == nil {
			c.Discrepancies = map[string]Discrepancy{}
		}
		c.Discrepancies[d.ID] = d
	case "REVIEW_RESULT":
		var payload struct {
			Pass bool `json:"pass"`
		}
		decodeEventData(e.Data, &payload)
		if payload.Pass {
			c.Status = ReviewPending
		} else {
			c.Status = Conditioned
		}
	case "RELEASE_DECISION":
		var payload struct {
			Approved         bool              `json:"approved"`
			Reason           string            `json:"reason"`
			By               string            `json:"by"`
			RemediationRound *RemediationRound `json:"remediation_round"`
		}
		decodeEventData(e.Data, &payload)
		if payload.Approved {
			c.Status = Released
		} else {
			c.Status = Tested
			if payload.RemediationRound != nil {
				c.Remediations = append(c.Remediations, *payload.RemediationRound)
			}
		}
	case "SEALED":
		var payload struct {
			SealedBy string `json:"sealed_by"`
		}
		decodeEventData(e.Data, &payload)
		c.Status = Sealed
		c.SealedAt = e.At
	}
	c.Revision = e.Revision
	return nil
}

func recomputeDiscrepancies(c *Case) {
	groups := make([]string, 0, len(c.Measurements))
	for g := range c.Measurements {
		groups = append(groups, g)
	}
	if len(groups) != 2 {
		return
	}
	sort.Strings(groups)
	a, b := c.Measurements[groups[0]], c.Measurements[groups[1]]
	diffs := []struct {
		n string
		v float64
	}{{"tensile", abs(a.Tensile - b.Tensile)}, {"ph", abs(a.PH - b.PH)}, {"color_delta_e", abs(a.ColorDelta - b.ColorDelta)}, {"fiber_change", abs(a.FiberChange - b.FiberChange)}}
	for _, d := range diffs {
		if d.v > 0.1 {
			c.Discrepancies[d.n] = Discrepancy{ID: d.n, Metric: d.n, Original: []float64{d.v}}
		} else {
			delete(c.Discrepancies, d.n)
		}
	}
}

func decodeEventData(data any, v any) {
	b, _ := json.Marshal(data)
	_ = json.Unmarshal(b, v)
}
