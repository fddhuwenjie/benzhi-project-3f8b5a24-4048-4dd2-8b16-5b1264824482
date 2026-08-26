package domain

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

type Status string

const (
	Draft         Status = "DRAFT"
	PlanReady     Status = "PLAN_READY"
	Conditioned   Status = "CONDITIONED"
	Tested        Status = "TESTED"
	ReviewPending Status = "REVIEW_PENDING"
	Released      Status = "RELEASED"
	Sealed        Status = "SEALED"
)

type Measurement struct {
	ID          string            `json:"id"`
	Group       string            `json:"group"`
	MeasuredBy  string            `json:"measured_by"`
	Tensile     float64           `json:"tensile"`
	PH          float64           `json:"ph_value"`
	ColorDelta  float64           `json:"color_delta_e"`
	FiberChange float64           `json:"fiber_dimension_change_rate"`
	Units       map[string]string `json:"units"`
	MeasuredAt  time.Time         `json:"measured_at"`
}
type Discrepancy struct {
	ID        string    `json:"id"`
	Metric    string    `json:"metric"`
	Reason    string    `json:"reason"`
	Decision  string    `json:"decision"`
	Reviewer  string    `json:"reviewer"`
	Original  []float64 `json:"original"`
	Retest    []float64 `json:"retest"`
	HasRetest bool      `json:"has_retest"`
	DecidedAt time.Time `json:"decided_at,omitempty"`
}
type Plan struct {
	ID          string             `json:"id,omitempty"`
	Groups      []string           `json:"groups"`
	TempMin     float64            `json:"temp_min"`
	TempMax     float64            `json:"temp_max"`
	HumMin      float64            `json:"hum_min"`
	HumMax      float64            `json:"hum_max"`
	MinExposure int                `json:"min_exposure"`
	Metrics     []string           `json:"metrics"`
	Thresholds  map[string]float64 `json:"thresholds"`
	SubmittedBy string             `json:"submitted_by"`
	SubmittedAt time.Time          `json:"submitted_at"`
}
type ConditioningReading struct {
	Temperature    float64   `json:"temperature"`
	Humidity       float64   `json:"humidity"`
	ExposedMinutes int       `json:"exposed_minutes"`
	At             time.Time `json:"at"`
}
type RemediationRound struct {
	Round                         int       `json:"round"`
	RejectedReleaseRevision       int       `json:"rejected_release_revision"`
	Reason                        string    `json:"reason"`
	FailedThresholds              []string  `json:"failed_thresholds"`
	SigningSnapshotHash           string    `json:"signing_snapshot_hash"`
	ThresholdsSummaryHash         string    `json:"thresholds_summary_hash"`
	SupersedesMeasurementRevision int       `json:"supersedes_measurement_revision"`
	ReplacementRevision           int       `json:"replacement_revision,omitempty"`
	RemediationNote               string    `json:"remediation_note,omitempty"`
	RejectedAt                    time.Time `json:"rejected_at"`
}
type Case struct {
	ID            string                 `json:"case_id"`
	Batch         string                 `json:"batch"`
	Material      string                 `json:"material"`
	Purpose       string                 `json:"purpose"`
	Owner         string                 `json:"owner"`
	Reviewer      string                 `json:"reviewer"`
	Authorizer    string                 `json:"authorizer"`
	Status        Status                 `json:"status"`
	Revision      int                    `json:"revision"`
	CreatedAt     time.Time              `json:"created_at"`
	SealedAt      time.Time              `json:"sealed_at,omitempty"`
	Plan          *Plan                  `json:"plan,omitempty"`
	Conditioning  []ConditioningReading  `json:"conditioning,omitempty"`
	Measurements  map[string]Measurement `json:"measurements,omitempty"`
	Discrepancies map[string]Discrepancy `json:"discrepancies,omitempty"`
	Remediations  []RemediationRound     `json:"remediations,omitempty"`
	Events        []Event                `json:"events,omitempty"`
}
type Event struct {
	Revision  int       `json:"revision"`
	Type      string    `json:"type"`
	Data      any       `json:"data"`
	RequestID string    `json:"request_id"`
	At        time.Time `json:"at"`
	PrevHash  string    `json:"prev_hash"`
	Hash      string    `json:"hash"`
}

func NewCase(id, batch, material, purpose, owner, reviewer, authorizer string) (*Case, error) {
	if strings.TrimSpace(id) == "" || strings.TrimSpace(batch) == "" || strings.TrimSpace(material) == "" || strings.TrimSpace(purpose) == "" || strings.TrimSpace(owner) == "" || strings.TrimSpace(reviewer) == "" || strings.TrimSpace(authorizer) == "" {
		return nil, errors.New("必填字段缺失")
	}
	if owner == reviewer || owner == authorizer || reviewer == authorizer {
		return nil, errors.New("职责必须分离")
	}
	return &Case{ID: id, Batch: batch, Material: material, Purpose: purpose, Owner: owner, Reviewer: reviewer, Authorizer: authorizer, Status: Draft, Measurements: map[string]Measurement{}, Discrepancies: map[string]Discrepancy{}, CreatedAt: time.Now().UTC()}, nil
}
func (c *Case) AppendEvent(t string, d any, rid string) {
	e := Event{Revision: c.Revision + 1, Type: t, Data: d, RequestID: rid, At: time.Now().UTC()}
	prev := ""
	if len(c.Events) > 0 {
		prev = c.Events[len(c.Events)-1].Hash
	}
	e.PrevHash = prev
	e.Hash = ComputeEventHash(e)
	c.Events = append(c.Events, e)
	c.Revision = e.Revision
}
func (c *Case) SubmitPlan(p Plan, rid string) error {
	if c.Status != Draft {
		return errors.New("状态不允许提交方案")
	}
	// 完整 API 提交（带提交人/阈值）必须恰好覆盖四项标准指标；保留旧版内部调用的简化方案。
	strictPlan := p.SubmittedBy != "" || p.Thresholds != nil
	report := ValidatePlan(c, p, strictPlan)
	if !report.Valid {
		return errors.New(report.Errors[0].Message)
	}
	p = report.NormalizedPlan
	p.SubmittedAt = time.Now().UTC()
	c.Plan = &p
	c.Status = PlanReady
	c.AppendEvent("PLAN_SUBMITTED", p, rid)
	return nil
}
func (c *Case) AddConditioning(r ConditioningReading, rid string) error {
	if c.Status == Sealed || c.Status == Released {
		return errors.New("个案已只读")
	}
	if c.Status == Conditioned {
		return errors.New("条件化已确认，状态已锁定")
	}
	if c.Status != PlanReady {
		return errors.New("状态不允许条件化")
	}
	if r.ExposedMinutes <= 0 {
		return errors.New("读数时间或暴露时长无效")
	}
	if r.At.IsZero() {
		r.At = time.Now().UTC()
		if len(c.Conditioning) > 0 {
			previous := c.Conditioning[len(c.Conditioning)-1]
			r.At = previous.At.Add(time.Duration(previous.ExposedMinutes) * time.Minute)
		}
	}
	for _, existing := range c.Conditioning {
		if r.At.Equal(existing.At) {
			return errors.New("读数时间戳重复")
		}
	}
	if c.Plan == nil || r.Temperature < c.Plan.TempMin || r.Temperature > c.Plan.TempMax || r.Humidity < c.Plan.HumMin || r.Humidity > c.Plan.HumMax {
		return errors.New("读数超出方案范围")
	}
	c.Conditioning = append(c.Conditioning, r)
	sort.SliceStable(c.Conditioning, func(i, j int) bool { return c.Conditioning[i].At.Before(c.Conditioning[j].At) })
	c.AppendEvent("CONDITIONING_READING", r, rid)
	return nil
}
func (c *Case) ConfirmConditioning(rid string) error {
	return c.ConfirmConditioningWindow(time.Time{}, time.Time{}, rid)
}

func (c *Case) ConfirmConditioningWindow(from, to time.Time, rid string) error {
	if c.Status == Conditioned || c.Status == Released || c.Status == Sealed {
		return errors.New("条件化已确认，状态已锁定")
	}
	if c.Status != PlanReady {
		return errors.New("状态不允许确认")
	}
	summary := BuildConditioningSummary(c)
	if !summary.Confirmable {
		if summary.FirstIssue != nil {
			return fmt.Errorf("条件化窗口%s: %s", summary.FirstIssue.Kind, summary.FirstIssue.Suggestion)
		}
		return errors.New("暴露时长不足")
	}
	selected := summary.LongestWindow
	if !from.IsZero() || !to.IsZero() {
		if from.IsZero() || to.IsZero() || !to.After(from) {
			return errors.New("确认窗口边界无效")
		}
		found := false
		for _, window := range summary.Windows {
			if window.Start.Equal(from) && window.End.Equal(to) && window.Minutes >= c.Plan.MinExposure {
				selected, found = window, true
				break
			}
		}
		if !found {
			return errors.New("确认窗口与当前摘要不匹配")
		}
	}
	c.Status = Conditioned
	c.AppendEvent("CONDITIONED", map[string]any{"window_from": selected.Start, "window_to": selected.End, "total_minutes": selected.Minutes}, rid)
	return nil
}
func (c *Case) AddMeasurements(ms []Measurement, rid string) error {
	if c.Status != Conditioned {
		return errors.New("状态不允许检测")
	}
	if len(ms) != 2 {
		return errors.New("必须提供两组盲样")
	}
	groupsSeen := map[string]bool{}
	validated := make([]Measurement, 0, len(ms))
	for _, m := range ms {
		if groupsSeen[m.Group] {
			return errors.New("盲样组重复")
		}
		groupsSeen[m.Group] = true
		if c.Plan != nil {
			ok := false
			for _, g := range c.Plan.Groups {
				if g == m.Group {
					ok = true
				}
			}
			if !ok {
				return errors.New("盲样组与方案不一致")
			}
		}
		if m.Group == "" || m.MeasuredBy == "" {
			return errors.New("测量值无效")
		}
		for n, u := range m.Units {
			switch n {
			case "tensile":
				if u == "kN" {
					m.Tensile *= 1000
				} else if u != "N" && u != "" {
					return errors.New("未知 tensile 单位")
				}
			case "color_delta_e":
				if u != "ΔE" && u != "" {
					return errors.New("未知 color_delta_e 单位")
				}
			case "fiber_change", "fiber_dimension_change_rate":
				if u != "%" && u != "" {
					return errors.New("未知 fiber_change 单位")
				}
			case "ph":
				if u != "pH" && u != "" {
					return errors.New("未知 ph 单位")
				}
			default:
				return errors.New("未知测量指标单位")
			}
		}
		for n, v := range map[string]float64{"tensile": m.Tensile, "ph": m.PH, "color_delta_e": m.ColorDelta, "fiber_change": m.FiberChange} {
			if err := ValidateMetric(n, v); err != nil {
				return err
			}
		}
		if m.MeasuredAt.IsZero() {
			m.MeasuredAt = time.Now().UTC()
		}
		validated = append(validated, m)
	}
	if len(validated) != 2 {
		return errors.New("盲样不完整")
	}
	// 仅在整批校验通过后写入，避免产生半条测量记录。
	for _, m := range validated {
		c.Measurements[m.Group] = m
	}
	groups := make([]string, 0, len(c.Measurements))
	for g := range c.Measurements {
		groups = append(groups, g)
	}
	sort.Strings(groups)
	a, b := c.Measurements[groups[0]], c.Measurements[groups[1]]
	diffs := []struct {
		n string
		v float64
	}{{"tensile", abs(a.Tensile - b.Tensile)}, {"ph", abs(a.PH - b.PH)}, {"color_delta_e", abs(a.ColorDelta - b.ColorDelta)}, {"fiber_change", abs(a.FiberChange - b.FiberChange)}}
	for _, d := range diffs {
		if d.v > 0.1 {
			id := d.n
			c.Discrepancies[id] = Discrepancy{ID: id, Metric: id, Original: []float64{d.v}}
		}
	}
	c.Status = Tested
	c.AppendEvent("MEASUREMENTS_RECORDED", validated, rid)
	return nil
}
func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
func (c *Case) DecideDiscrepancy(d Discrepancy, rid string) error {
	if c.Status != Tested {
		return errors.New("状态不允许复测")
	}
	current, ok := c.Discrepancies[d.ID]
	if !ok {
		return errors.New("差异项不存在")
	}
	if current.Decision != "" {
		return errors.New("差异项已裁决")
	}
	if d.Metric == "" {
		d.Metric = current.Metric
	}
	if d.Metric != current.Metric {
		return errors.New("差异项与指标不一致")
	}
	if d.Reviewer == "" || d.Reviewer != c.Reviewer || d.Reviewer == c.Owner || d.Reviewer == c.Authorizer || d.Decision != "PASS" && d.Decision != "FAIL" || len(d.Retest) == 0 || strings.TrimSpace(d.Reason) == "" {
		return errors.New("复核职责冲突")
	}
	for _, measurement := range c.Measurements {
		if measurement.MeasuredBy == d.Reviewer {
			return errors.New("复核员必须独立于测量执行人")
		}
	}
	for _, v := range d.Retest {
		if err := ValidateMetric(d.Metric, v); err != nil {
			return err
		}
	}
	d.DecidedAt = time.Now().UTC()
	d.Original = append([]float64(nil), current.Original...)
	d.HasRetest = true
	c.Discrepancies[d.ID] = d
	c.AppendEvent("DISCREPANCY_DECIDED", d, rid)
	all := true
	pass := true
	for _, x := range c.Discrepancies {
		if x.Decision == "" {
			all = false
		}
		if x.Decision != "PASS" {
			pass = false
		}
	}
	if all {
		if pass {
			c.Status = ReviewPending
		} else {
			c.Status = Conditioned
		}
		c.AppendEvent("REVIEW_RESULT", map[string]any{"pass": pass}, rid)
	}
	return nil
}
func (c *Case) Release(approved bool, reason, by, rid string) error {
	if c.Status != ReviewPending && c.Status != Tested {
		return errors.New("状态不允许放行")
	}
	if by == "" || by != c.Authorizer || by == c.Owner || by == c.Reviewer {
		return errors.New("授权职责冲突")
	}
	if strings.TrimSpace(reason) == "" {
		return errors.New("放行决定原因不能为空")
	}
	if approved {
		for _, d := range c.Discrepancies {
			if d.Decision != "PASS" {
				return errors.New("差异项未全部通过")
			}
		}
	}
	preview := BuildReleasePreview(c)
	if approved {
		c.Status = Released
	} else {
		c.Status = Tested
		failed := make([]string, 0)
		for _, threshold := range preview.Thresholds {
			if !threshold.Passed {
				failed = append(failed, threshold.MetricCode)
			}
		}
		round := RemediationRound{
			Round: len(c.Remediations) + 1, RejectedReleaseRevision: c.Revision + 1,
			Reason: strings.TrimSpace(reason), FailedThresholds: failed,
			SigningSnapshotHash: preview.SnapshotHash, ThresholdsSummaryHash: preview.ThresholdsSummaryHash,
			SupersedesMeasurementRevision: c.latestMeasurementRevision(), RejectedAt: time.Now().UTC(),
		}
		c.Remediations = append(c.Remediations, round)
	}
	payload := map[string]any{"approved": approved, "reason": reason, "by": by, "thresholds_summary_hash": preview.ThresholdsSummaryHash, "snapshot_hash": preview.SnapshotHash, "signed_at": time.Now().UTC()}
	if !approved {
		payload["remediation_round"] = c.Remediations[len(c.Remediations)-1]
	}
	c.AppendEvent("RELEASE_DECISION", payload, rid)
	return nil
}

func CloneCase(c *Case) *Case {
	if c == nil {
		return nil
	}
	b, _ := json.Marshal(c)
	var out Case
	_ = json.Unmarshal(b, &out)
	if out.Measurements == nil {
		out.Measurements = map[string]Measurement{}
	}
	if out.Discrepancies == nil {
		out.Discrepancies = map[string]Discrepancy{}
	}
	return &out
}
func (c *Case) Seal(by, rid string) error {
	if c.Status != Released {
		return errors.New("仅已放行个案可封存")
	}
	if strings.TrimSpace(by) == "" || by == c.Owner || by == c.Reviewer {
		return errors.New("封存职责冲突")
	}
	c.Status = Sealed
	c.SealedAt = time.Now().UTC()
	c.AppendEvent("SEALED", map[string]any{"sealed_by": by}, rid)
	return nil
}
