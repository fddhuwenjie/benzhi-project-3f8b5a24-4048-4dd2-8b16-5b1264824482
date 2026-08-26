package application

import (
	"encoding/json"
	"errors"
	"fmt"
	"guji-paper/internal/audit"
	"guji-paper/internal/domain"
	"sort"
	"time"
)

func Snapshot(c *domain.Case) domain.Status {
	if c == nil {
		return ""
	}
	return c.Status
}

type StatusWorkload struct {
	Total int `json:"total"`
	Stale int `json:"stale"`
}

type CaseList struct {
	Cases        []any                            `json:"cases"`
	Total        int                              `json:"total"`
	StatusCounts map[domain.Status]int            `json:"status_counts"`
	StatusStats  map[domain.Status]StatusWorkload `json:"status_stats"`
	StaleTotal   int                              `json:"stale_total"`
	Page         int                              `json:"page"`
	PageSize     int                              `json:"page_size"`
}

type CaseListFilter struct {
	Status       string
	Purpose      string
	Owner        string
	Batch        string
	CreatedFrom  *time.Time
	CreatedTo    *time.Time
	StaleMinutes int
	Page         int
	PageSize     int
	Now          time.Time
}

func (s *Service) List(status, purpose, owner, batch string, page, size int) (CaseList, error) {
	return s.ListCases(CaseListFilter{Status: status, Purpose: purpose, Owner: owner, Batch: batch, Page: page, PageSize: size})
}

func (s *Service) ListCases(filter CaseListFilter) (CaseList, error) {
	if filter.Page < 1 || filter.PageSize < 1 || filter.PageSize > 100 {
		return CaseList{}, fmt.Errorf("分页参数无效")
	}
	if filter.Status != "" && !domain.ValidStatus(domain.Status(filter.Status)) {
		return CaseList{}, fmt.Errorf("未知 status")
	}
	if filter.StaleMinutes < 0 {
		return CaseList{}, fmt.Errorf("stale_minutes 必须为非负整数")
	}
	if filter.CreatedFrom != nil && filter.CreatedTo != nil && filter.CreatedTo.Before(*filter.CreatedFrom) {
		return CaseList{}, fmt.Errorf("created_to 不得早于 created_from")
	}
	if filter.Now.IsZero() {
		filter.Now = time.Now().UTC()
	}
	all := s.st.All()
	filtered := make([]*domain.Case, 0)
	staleByID := map[string]int{}
	counts := map[domain.Status]int{}
	stats := map[domain.Status]StatusWorkload{}
	staleTotal := 0
	for _, c := range all {
		if filter.Status != "" && string(c.Status) != filter.Status || filter.Purpose != "" && c.Purpose != filter.Purpose || filter.Owner != "" && c.Owner != filter.Owner || filter.Batch != "" && c.Batch != filter.Batch {
			continue
		}
		if filter.CreatedFrom != nil && c.CreatedAt.Before(*filter.CreatedFrom) || filter.CreatedTo != nil && c.CreatedAt.After(*filter.CreatedTo) {
			continue
		}
		lastActivity := c.CreatedAt
		if len(c.Events) > 0 && c.Events[len(c.Events)-1].At.After(lastActivity) {
			lastActivity = c.Events[len(c.Events)-1].At
		}
		stale := int(filter.Now.Sub(lastActivity) / time.Minute)
		if stale < 0 {
			stale = 0
		}
		if filter.StaleMinutes > 0 && stale < filter.StaleMinutes {
			continue
		}
		filtered = append(filtered, c)
		staleByID[c.ID] = stale
		counts[c.Status]++
		item := stats[c.Status]
		item.Total++
		if filter.StaleMinutes > 0 && stale >= filter.StaleMinutes {
			item.Stale++
			staleTotal++
		}
		stats[c.Status] = item
	}
	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].CreatedAt.Equal(filtered[j].CreatedAt) {
			return filtered[i].ID < filtered[j].ID
		}
		return filtered[i].CreatedAt.Before(filtered[j].CreatedAt)
	})
	total := len(filtered)
	start := (filter.Page - 1) * filter.PageSize
	if start > total {
		start = total
	}
	end := start + filter.PageSize
	if end > total {
		end = total
	}
	out := filtered[start:end]
	safe := make([]any, len(out))
	for i, c := range out {
		if c.Status == domain.Sealed {
			var manifest any
			if value, ok := s.st.Manifest(c.ID); ok {
				manifest = map[string]any{"manifest_id": value.ManifestID, "manifest_sha256": value.ManifestSHA256, "event_chain_root": value.EventChainRoot, "verified": value.Verified}
			}
			safe[i] = map[string]any{"case_id": c.ID, "status": c.Status, "revision": c.Revision, "created_at": c.CreatedAt, "sealed_at": c.SealedAt, "stale_minutes": staleByID[c.ID], "manifest": manifest}
			continue
		}
		b, _ := json.Marshal(c)
		var item map[string]any
		_ = json.Unmarshal(b, &item)
		item["stale_minutes"] = staleByID[c.ID]
		safe[i] = item
	}
	return CaseList{Cases: safe, Total: total, StatusCounts: counts, StatusStats: stats, StaleTotal: staleTotal, Page: filter.Page, PageSize: filter.PageSize}, nil
}

func (s *Service) ConditioningSummary(id string) (domain.ConditioningSummary, error) {
	c := s.st.Get(id)
	if c == nil {
		return domain.ConditioningSummary{}, errors.New("个案不存在")
	}
	return domain.BuildConditioningSummary(c), nil
}

func (s *Service) MeasurementReport(id string, expectedMeasurementRevision int) (domain.MeasurementReport, error) {
	c := s.st.Get(id)
	if c == nil {
		return domain.MeasurementReport{}, errors.New("个案不存在")
	}
	events := s.st.Events(id)
	chain := audit.VerifyEventRange(events, 1, 0)
	var measurementEvent *domain.Event
	for i := range events {
		if events[i].Type == "MEASUREMENTS_RECORDED" || events[i].Type == "REMEDIATION_MEASUREMENTS_RECORDED" {
			measurementEvent = &events[i]
		}
	}
	if measurementEvent == nil {
		return domain.MeasurementReport{}, errors.New("测量报告不存在")
	}
	report := domain.BuildMeasurementReport(c, measurementEvent.Revision, measurementEvent.PrevHash, measurementEvent.Hash)
	if expectedMeasurementRevision > 0 && expectedMeasurementRevision != measurementEvent.Revision {
		report.Verified, report.Error = false, "measurement_revision_mismatch"
		return report, errors.New("测量 revision 不匹配")
	}
	if !chain.Verified || c.Revision != len(events) {
		report.Verified, report.Error = false, "event_chain_unverifiable"
		return report, errors.New("测量报告不可验证")
	}
	var recorded []domain.Measurement
	b, _ := json.Marshal(measurementEvent.Data)
	if measurementEvent.Type == "REMEDIATION_MEASUREMENTS_RECORDED" {
		var remediation struct {
			Measurements []domain.Measurement `json:"measurements"`
		}
		if json.Unmarshal(b, &remediation) == nil {
			recorded = remediation.Measurements
		}
	} else {
		_ = json.Unmarshal(b, &recorded)
	}
	if !measurementsMatch(c.Measurements, recorded) {
		report.Verified, report.Error = false, "measurement_snapshot_mismatch"
		return report, errors.New("测量报告不可验证")
	}
	return report, nil
}

func measurementsMatch(current map[string]domain.Measurement, recorded []domain.Measurement) bool {
	if len(current) != len(recorded) {
		return false
	}
	byGroup := map[string]domain.Measurement{}
	for _, measurement := range recorded {
		byGroup[measurement.Group] = measurement
	}
	left, _ := json.Marshal(current)
	right, _ := json.Marshal(byGroup)
	return string(left) == string(right)
}

func (s *Service) DiscrepancyProgress(id string) (domain.DiscrepancyProgress, error) {
	c := s.st.Get(id)
	if c == nil {
		return domain.DiscrepancyProgress{}, errors.New("个案不存在")
	}
	return domain.BuildDiscrepancyProgress(c), nil
}

func (s *Service) ReleasePreview(id string) (domain.ReleasePreview, error) {
	c := s.st.Get(id)
	if c == nil {
		return domain.ReleasePreview{}, errors.New("个案不存在")
	}
	return domain.BuildReleasePreview(c), nil
}

type ManifestPage struct {
	ManifestID     string                     `json:"manifest_id"`
	CaseID         string                     `json:"case_id"`
	CaseRevision   int                        `json:"case_revision"`
	EventChainRoot string                     `json:"event_chain_root"`
	ManifestSHA256 string                     `json:"manifest_sha256"`
	Verified       bool                       `json:"verified"`
	SealedBy       string                     `json:"sealed_by"`
	Entries        []string                   `json:"entries,omitempty"`
	Total          int                        `json:"total"`
	Page           int                        `json:"page"`
	PageSize       int                        `json:"page_size"`
	Verification   audit.ManifestVerification `json:"verification"`
}

type EvidenceQuery struct {
	Stage                   string
	FromRevision            int
	ToRevision              int
	Page                    int
	PageSize                int
	ExpectedManifestSHA256  string
	ExpectedEventChainRoot  string
	ExpectedSelectionSHA256 string
}

type EvidenceCredential struct {
	CaseID               string               `json:"case_id"`
	EvidenceStage        string               `json:"evidence_stage"`
	FromRevision         int                  `json:"from_revision"`
	ToRevision           int                  `json:"to_revision"`
	Items                []audit.EvidenceItem `json:"items"`
	Total                int                  `json:"total"`
	Page                 int                  `json:"page"`
	PageSize             int                  `json:"page_size"`
	SelectionSHA256      string               `json:"selection_sha256"`
	ManifestSHA256       string               `json:"manifest_sha256"`
	EventChainRoot       string               `json:"event_chain_root"`
	CaseRevision         int                  `json:"case_revision"`
	ManifestVerified     bool                 `json:"manifest_verified"`
	EventChainVerified   bool                 `json:"event_chain_verified"`
	ManifestSHA256Match  bool                 `json:"manifest_sha256_match"`
	EventChainRootMatch  bool                 `json:"event_chain_root_match"`
	SelectionSHA256Match bool                 `json:"selection_sha256_match"`
	FirstInvalidRevision int                  `json:"first_invalid_revision,omitempty"`
	Error                string               `json:"error,omitempty"`
}

func (s *Service) EvidenceCredential(id string, query EvidenceQuery) (EvidenceCredential, error) {
	out := EvidenceCredential{CaseID: id, EvidenceStage: query.Stage, FromRevision: query.FromRevision, ToRevision: query.ToRevision, Page: query.Page, PageSize: query.PageSize, Items: []audit.EvidenceItem{}}
	stage, validStage := domain.NormalizeEvidenceStage(query.Stage)
	if !validStage {
		return out, errors.New("未知 evidence_stage")
	}
	query.Stage, out.EvidenceStage = stage, stage
	if query.Page < 1 || query.PageSize < 1 || query.PageSize > 100 || query.FromRevision < 1 || query.ToRevision > 0 && query.ToRevision < query.FromRevision {
		return out, errors.New("核验查询参数无效")
	}
	c := s.st.Get(id)
	if c == nil {
		return out, errors.New("个案不存在")
	}
	if c.Status != domain.Sealed {
		return out, errors.New("个案尚未封存")
	}
	if query.ToRevision == 0 {
		query.ToRevision = c.Revision
		out.ToRevision = c.Revision
	}
	if query.ToRevision > c.Revision {
		return out, errors.New("核验修订区间超出封存终态")
	}
	m, ok := s.st.Manifest(id)
	if !ok {
		return out, errors.New("封存清单不存在")
	}
	selection, err := audit.SelectEvidence(m, s.st.Events(id), c.Revision, audit.EvidenceFilter{Stage: query.Stage, FromRevision: query.FromRevision, ToRevision: query.ToRevision})
	out.ManifestSHA256, out.EventChainRoot, out.CaseRevision = selection.ManifestSHA256, selection.EventChainRoot, selection.CaseRevision
	out.ManifestVerified, out.EventChainVerified, out.FirstInvalidRevision = selection.ManifestVerified, selection.EventChainVerified, selection.FirstInvalidRevision
	if err != nil {
		out.Error = err.Error()
		return out, err
	}
	out.Total, out.SelectionSHA256 = selection.Total, selection.SelectionSHA256
	out.ManifestSHA256Match = query.ExpectedManifestSHA256 == "" || query.ExpectedManifestSHA256 == out.ManifestSHA256
	out.EventChainRootMatch = query.ExpectedEventChainRoot == "" || query.ExpectedEventChainRoot == out.EventChainRoot
	out.SelectionSHA256Match = query.ExpectedSelectionSHA256 == "" || query.ExpectedSelectionSHA256 == out.SelectionSHA256
	start := (query.Page - 1) * query.PageSize
	if start > len(selection.Items) {
		start = len(selection.Items)
	}
	end := start + query.PageSize
	if end > len(selection.Items) {
		end = len(selection.Items)
	}
	out.Items = append(out.Items, selection.Items[start:end]...)
	if !out.ManifestSHA256Match || !out.EventChainRootMatch || !out.SelectionSHA256Match {
		out.Error = "预期摘要不匹配"
	}
	return out, nil
}

func (s *Service) ManifestPage(id string, page, size int, includeEntries bool) (ManifestPage, error) {
	if page < 1 || size < 1 || size > 100 {
		return ManifestPage{}, errors.New("分页参数无效")
	}
	c := s.st.Get(id)
	if c == nil {
		return ManifestPage{}, errors.New("个案不存在")
	}
	if c.Status != domain.Sealed {
		return ManifestPage{}, errors.New("个案尚未封存")
	}
	m, ok := s.st.Manifest(id)
	if !ok {
		return ManifestPage{}, errors.New("封存清单不存在")
	}
	verification := audit.VerifyManifest(m, s.st.Events(id), c.Revision)
	out := ManifestPage{ManifestID: m.ManifestID, CaseID: m.CaseID, CaseRevision: m.CaseRevision, EventChainRoot: m.EventChainRoot, ManifestSHA256: m.ManifestSHA256, Verified: verification.Verified, SealedBy: m.SealedBy, Total: len(m.Entries), Page: page, PageSize: size, Verification: verification}
	if includeEntries {
		start := (page - 1) * size
		if start > len(m.Entries) {
			start = len(m.Entries)
		}
		end := start + size
		if end > len(m.Entries) {
			end = len(m.Entries)
		}
		out.Entries = append([]string(nil), m.Entries[start:end]...)
	}
	return out, nil
}

type AuditQuery struct {
	EventType    string
	RequestID    string
	FromRevision int
	ToRevision   int
	Page         int
	PageSize     int
}

type AuditPage struct {
	Events               []domain.Event `json:"events"`
	Total                int            `json:"total"`
	UnfilteredTotal      int            `json:"unfiltered_total"`
	Page                 int            `json:"page"`
	PageSize             int            `json:"page_size"`
	Verified             bool           `json:"verified"`
	FirstInvalidRevision int            `json:"first_invalid_revision,omitempty"`
	Error                string         `json:"error,omitempty"`
	RangeFirstPrevHash   string         `json:"range_first_prev_hash,omitempty"`
	RangeFirstHash       string         `json:"range_first_hash,omitempty"`
	RangeLastHash        string         `json:"range_last_hash,omitempty"`
}

func (s *Service) Audit(id string, query AuditQuery) (AuditPage, error) {
	if query.Page < 1 || query.PageSize < 1 || query.PageSize > 100 || query.FromRevision < 1 || query.ToRevision > 0 && query.ToRevision < query.FromRevision {
		return AuditPage{}, errors.New("审计查询参数无效")
	}
	events := s.st.Events(id)
	if s.st.Get(id) == nil {
		return AuditPage{}, errors.New("个案不存在")
	}
	rangeEvents := make([]domain.Event, 0)
	for _, event := range events {
		if event.Revision >= query.FromRevision && (query.ToRevision == 0 || event.Revision <= query.ToRevision) {
			rangeEvents = append(rangeEvents, event)
		}
	}
	report := audit.VerifyEventRange(events, query.FromRevision, query.ToRevision)
	filtered := make([]domain.Event, 0)
	for _, event := range rangeEvents {
		if query.EventType != "" && event.Type != query.EventType || query.RequestID != "" && event.RequestID != query.RequestID {
			continue
		}
		filtered = append(filtered, event)
	}
	start := (query.Page - 1) * query.PageSize
	if start > len(filtered) {
		start = len(filtered)
	}
	end := start + query.PageSize
	if end > len(filtered) {
		end = len(filtered)
	}
	page := AuditPage{Events: append([]domain.Event(nil), filtered[start:end]...), Total: len(filtered), UnfilteredTotal: len(rangeEvents), Page: query.Page, PageSize: query.PageSize, Verified: report.Verified, FirstInvalidRevision: report.FirstInvalidRevision, Error: report.Error, RangeFirstPrevHash: report.RangeFirstPrevHash, RangeFirstHash: report.RangeFirstHash, RangeLastHash: report.RangeLastHash}
	if !report.Verified {
		return page, errors.New("审计区间校验失败")
	}
	return page, nil
}
