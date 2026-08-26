package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

type ValidationIssue struct {
	Field   string `json:"field"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type PlanValidationReport struct {
	Valid          bool              `json:"valid"`
	CanSubmit      bool              `json:"can_submit"`
	CurrentStatus  Status            `json:"current_status"`
	NextStatus     Status            `json:"next_status,omitempty"`
	Revision       int               `json:"revision"`
	NormalizedPlan Plan              `json:"normalized_plan"`
	Errors         []ValidationIssue `json:"errors"`
}

func ValidatePlan(c *Case, p Plan, strict bool) PlanValidationReport {
	originalGroups := append([]string(nil), p.Groups...)
	originalMetrics := append([]string(nil), p.Metrics...)
	p.Groups = normalizeUnique(p.Groups)
	p.Metrics = normalizeMetrics(p.Metrics)
	p.SubmittedBy = strings.TrimSpace(p.SubmittedBy)
	p.SubmittedAt = time.Time{}
	report := PlanValidationReport{CurrentStatus: c.Status, Revision: c.Revision, NormalizedPlan: p, Errors: []ValidationIssue{}}
	add := func(field, code, message string) {
		report.Errors = append(report.Errors, ValidationIssue{Field: field, Code: code, Message: message})
	}
	if c.Status != Draft {
		add("status", "invalid_state", "状态不允许提交方案")
	}
	if len(p.Groups) != 2 {
		add("groups", "invalid_group_count", "盲样组必须恰好为两组且名称不同")
	}
	if len(originalGroups) != len(p.Groups) {
		add("groups", "duplicate_group", "盲样组名称不能为空或重复")
	}
	if p.MinExposure <= 0 {
		add("min_exposure", "out_of_range", "最小暴露分钟数必须大于零")
	}
	if !finite(p.TempMin) || !finite(p.TempMax) || p.TempMin >= p.TempMax {
		add("temperature", "invalid_range", "温度范围无效")
	}
	if !finite(p.HumMin) || !finite(p.HumMax) || p.HumMin >= p.HumMax {
		add("humidity", "invalid_range", "湿度范围无效")
	}
	seen := map[string]bool{}
	for _, name := range p.Metrics {
		if metricRule(name) == nil {
			add("metrics."+name, "unknown_metric", "未知指标 "+name)
			continue
		}
		seen[name] = true
	}
	if len(originalMetrics) != len(p.Metrics) {
		add("metrics", "duplicate_metric", "检测指标不能为空或重复")
	}
	if strict {
		for _, rule := range DefaultMetricRules {
			if !seen[rule.Name] {
				add("metrics."+rule.Name, "missing_metric", "缺少指标 "+rule.Name)
			}
			value, ok := p.Thresholds[rule.Name]
			if !ok {
				add("thresholds."+rule.Name, "missing_threshold", "缺少指标阈值 "+rule.Name)
			} else if !finite(value) || value < rule.Min || value > rule.Max {
				add("thresholds."+rule.Name, "out_of_range", fmt.Sprintf("%s 阈值必须在 %g 至 %g 之间", rule.Name, rule.Min, rule.Max))
			}
		}
		for name := range p.Thresholds {
			if metricRule(name) == nil {
				add("thresholds."+name, "unknown_metric", "未知指标阈值 "+name)
			}
		}
	}
	if strict && p.SubmittedBy == "" {
		add("submitted_by", "required", "提交人不能为空")
	} else if p.SubmittedBy != "" && (p.SubmittedBy == c.Owner || p.SubmittedBy == c.Reviewer || p.SubmittedBy == c.Authorizer) {
		add("submitted_by", "duty_conflict", "提交人职责冲突")
	}
	report.Valid = len(report.Errors) == 0
	report.CanSubmit = report.Valid
	if report.Valid {
		report.NextStatus = PlanReady
	}
	return report
}

func normalizeUnique(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	sort.Strings(out)
	return out
}

func normalizeMetrics(values []string) []string {
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			seen[value] = true
		}
	}
	out := make([]string, 0, len(seen))
	for _, rule := range DefaultMetricRules {
		if seen[rule.Name] {
			out = append(out, rule.Name)
			delete(seen, rule.Name)
		}
	}
	extra := make([]string, 0, len(seen))
	for value := range seen {
		extra = append(extra, value)
	}
	sort.Strings(extra)
	return append(out, extra...)
}

func finite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }

func metricRule(name string) *MetricRule {
	for i := range DefaultMetricRules {
		if DefaultMetricRules[i].Name == name {
			return &DefaultMetricRules[i]
		}
	}
	return nil
}

type ConditioningSegment struct {
	Index        int       `json:"index"`
	At           time.Time `json:"at"`
	CoveredUntil time.Time `json:"covered_until"`
	Minutes      int       `json:"minutes"`
	Compliant    bool      `json:"compliant"`
	Issue        string    `json:"issue,omitempty"`
}

type ConditioningWindow struct {
	Start   time.Time `json:"start"`
	End     time.Time `json:"end"`
	Minutes int       `json:"minutes"`
}

type ConditioningIssue struct {
	Index      int       `json:"index"`
	Kind       string    `json:"kind"`
	From       time.Time `json:"from,omitempty"`
	To         time.Time `json:"to,omitempty"`
	Suggestion string    `json:"suggestion"`
}

type ConditioningSummary struct {
	CaseID                    string                `json:"case_id"`
	Revision                  int                   `json:"revision"`
	Status                    Status                `json:"status"`
	Segments                  []ConditioningSegment `json:"segments"`
	Windows                   []ConditioningWindow  `json:"windows"`
	CumulativeExposureMinutes int                   `json:"cumulative_exposure_minutes"`
	LongestContinuousMinutes  int                   `json:"longest_continuous_minutes"`
	LongestWindow             ConditioningWindow    `json:"longest_window"`
	RequiredMinutes           int                   `json:"required_minutes"`
	Confirmable               bool                  `json:"confirmable"`
	Locked                    bool                  `json:"locked"`
	FirstIssue                *ConditioningIssue    `json:"first_issue,omitempty"`
}

func BuildConditioningSummary(c *Case) ConditioningSummary {
	s := ConditioningSummary{CaseID: c.ID, Revision: c.Revision, Status: c.Status, Segments: []ConditioningSegment{}, Windows: []ConditioningWindow{}, Locked: c.Status == Conditioned || c.Status == Released || c.Status == Sealed}
	if c.Plan == nil {
		s.FirstIssue = &ConditioningIssue{Kind: "missing_plan", Suggestion: "请先提交判定方案"}
		return s
	}
	s.RequiredMinutes = c.Plan.MinExposure
	var current *ConditioningWindow
	for i, reading := range c.Conditioning {
		end := reading.At.Add(time.Duration(reading.ExposedMinutes) * time.Minute)
		segment := ConditioningSegment{Index: i, At: reading.At, CoveredUntil: end, Minutes: reading.ExposedMinutes, Compliant: true}
		var issue *ConditioningIssue
		switch {
		case reading.At.IsZero() || reading.ExposedMinutes <= 0:
			segment.Compliant, segment.Issue = false, "invalid_reading"
			issue = &ConditioningIssue{Index: i, Kind: "读数无效", Suggestion: "请补录有效时间和暴露时长"}
		case reading.Temperature < c.Plan.TempMin || reading.Temperature > c.Plan.TempMax || reading.Humidity < c.Plan.HumMin || reading.Humidity > c.Plan.HumMax:
			segment.Compliant, segment.Issue = false, "out_of_range"
			issue = &ConditioningIssue{Index: i, Kind: "超出范围", From: reading.At, To: end, Suggestion: "请在方案温湿度范围内重新采集该时段读数"}
		case i > 0 && reading.At.Equal(c.Conditioning[i-1].At):
			segment.Compliant, segment.Issue = false, "duplicate_timestamp"
			issue = &ConditioningIssue{Index: i, Kind: "重复时间戳", From: reading.At, To: reading.At, Suggestion: "请删除重复读数或使用正确采集时间补录"}
		case i > 0 && reading.At.Before(c.Conditioning[i-1].At):
			segment.Compliant, segment.Issue = false, "out_of_order"
			issue = &ConditioningIssue{Index: i, Kind: "时间逆序", From: reading.At, To: c.Conditioning[i-1].At, Suggestion: "请按采集时间顺序补录读数"}
		case i > 0:
			previousEnd := c.Conditioning[i-1].At.Add(time.Duration(c.Conditioning[i-1].ExposedMinutes) * time.Minute)
			if reading.At.After(previousEnd) {
				issue = &ConditioningIssue{Index: i, Kind: "存在缺口", From: previousEnd, To: reading.At, Suggestion: "请补录缺口时段的合规读数"}
			}
		}
		s.Segments = append(s.Segments, segment)
		if s.FirstIssue == nil && issue != nil {
			s.FirstIssue = issue
		}
		if !segment.Compliant {
			if current != nil {
				s.Windows = append(s.Windows, *current)
				current = nil
			}
			continue
		}
		s.CumulativeExposureMinutes += reading.ExposedMinutes
		if current == nil || reading.At.After(current.End) {
			if current != nil {
				s.Windows = append(s.Windows, *current)
			}
			current = &ConditioningWindow{Start: reading.At, End: end}
		} else if end.After(current.End) {
			current.End = end
		}
		current.Minutes = int(current.End.Sub(current.Start) / time.Minute)
	}
	if current != nil {
		s.Windows = append(s.Windows, *current)
	}
	for _, window := range s.Windows {
		if window.Minutes > s.LongestContinuousMinutes {
			s.LongestContinuousMinutes = window.Minutes
			s.LongestWindow = window
		}
	}
	s.Confirmable = !s.Locked && s.LongestContinuousMinutes >= s.RequiredMinutes
	return s
}

type MeasurementValue struct {
	Group        string  `json:"group"`
	RawValue     float64 `json:"raw_value"`
	RawUnit      string  `json:"raw_unit"`
	Value        float64 `json:"value"`
	StandardUnit string  `json:"standard_unit"`
	Threshold    float64 `json:"threshold"`
	Margin       float64 `json:"margin"`
	Passed       bool    `json:"passed"`
}

type MetricComparison struct {
	MetricCode        string             `json:"metric_code"`
	Values            []MeasurementValue `json:"values"`
	Difference        float64            `json:"difference"`
	DiscrepancyID     string             `json:"discrepancy_id,omitempty"`
	DiscrepancySource string             `json:"discrepancy_source,omitempty"`
	Issues            []ValidationIssue  `json:"issues"`
}

type MeasurementReport struct {
	CaseID                 string             `json:"case_id"`
	Status                 Status             `json:"status"`
	CaseRevision           int                `json:"case_revision"`
	MeasurementRevision    int                `json:"measurement_revision"`
	EventChainPreviousHash string             `json:"event_chain_previous_hash"`
	EventHash              string             `json:"event_hash"`
	Verified               bool               `json:"verified"`
	Error                  string             `json:"error,omitempty"`
	Metrics                []MetricComparison `json:"metrics,omitempty"`
}

func BuildMeasurementReport(c *Case, measurementRevision int, previousHash, eventHash string) MeasurementReport {
	report := MeasurementReport{CaseID: c.ID, Status: c.Status, CaseRevision: c.Revision, MeasurementRevision: measurementRevision, EventChainPreviousHash: previousHash, EventHash: eventHash, Verified: true, Metrics: []MetricComparison{}}
	groups := make([]string, 0, len(c.Measurements))
	for group := range c.Measurements {
		groups = append(groups, group)
	}
	sort.Strings(groups)
	if len(groups) != 2 || c.Plan == nil {
		report.Verified, report.Error = false, "measurement_snapshot_incomplete"
		return report
	}
	for _, rule := range DefaultMetricRules {
		comparison := MetricComparison{MetricCode: rule.Name, Values: []MeasurementValue{}, Issues: []ValidationIssue{}}
		values := make([]float64, 0, 2)
		for _, group := range groups {
			measurement := c.Measurements[group]
			value := measurementMetric(measurement, rule.Name)
			rawUnit := measurement.Units[rule.Name]
			if rule.Name == "fiber_change" && rawUnit == "" {
				rawUnit = measurement.Units["fiber_change"]
			}
			if rawUnit == "" {
				rawUnit = rule.Unit
			}
			rawValue := value
			if rule.Name == "tensile" && rawUnit == "kN" {
				rawValue = value / 1000
			}
			threshold := c.Plan.Thresholds[rule.Name]
			margin, passed := thresholdMargin(rule.Name, value, threshold)
			comparison.Values = append(comparison.Values, MeasurementValue{Group: group, RawValue: rawValue, RawUnit: rawUnit, Value: value, StandardUnit: rule.Unit, Threshold: threshold, Margin: margin, Passed: passed})
			if err := ValidateMetric(rule.Name, value); err != nil {
				comparison.Issues = append(comparison.Issues, ValidationIssue{Field: "measurements." + group + "." + rule.Name, Code: "out_of_range", Message: err.Error()})
			} else if !passed {
				comparison.Issues = append(comparison.Issues, ValidationIssue{Field: "measurements." + group + "." + rule.Name, Code: "threshold_failed", Message: "测量值未达到用途阈值"})
			}
			values = append(values, value)
		}
		comparison.Difference = abs(values[0] - values[1])
		if discrepancy, ok := c.Discrepancies[rule.Name]; ok {
			comparison.DiscrepancyID = discrepancy.ID
			comparison.DiscrepancySource = "group_difference"
		}
		report.Metrics = append(report.Metrics, comparison)
	}
	sort.Slice(report.Metrics, func(i, j int) bool { return report.Metrics[i].MetricCode < report.Metrics[j].MetricCode })
	return report
}

func measurementMetric(m Measurement, metric string) float64 {
	switch metric {
	case "tensile":
		return m.Tensile
	case "ph":
		return m.PH
	case "color_delta_e":
		return m.ColorDelta
	case "fiber_change":
		return m.FiberChange
	default:
		return math.NaN()
	}
}

func thresholdMargin(metric string, value, threshold float64) (float64, bool) {
	if metric == "tensile" {
		return value - threshold, value >= threshold
	}
	return threshold - value, value <= threshold
}

type DiscrepancyProgress struct {
	CaseID               string        `json:"case_id"`
	Revision             int           `json:"revision"`
	Status               Status        `json:"status"`
	Items                []Discrepancy `json:"items"`
	Total                int           `json:"total"`
	Completed            int           `json:"completed"`
	CompletionPercentage float64       `json:"completion_percentage"`
	PendingIDs           []string      `json:"pending_ids"`
}

func BuildDiscrepancyProgress(c *Case) DiscrepancyProgress {
	report := DiscrepancyProgress{CaseID: c.ID, Revision: c.Revision, Status: c.Status, Items: []Discrepancy{}, PendingIDs: []string{}}
	for _, discrepancy := range c.Discrepancies {
		discrepancy.HasRetest = len(discrepancy.Retest) > 0
		report.Items = append(report.Items, discrepancy)
	}
	sort.Slice(report.Items, func(i, j int) bool {
		if report.Items[i].Metric == report.Items[j].Metric {
			return report.Items[i].ID < report.Items[j].ID
		}
		return report.Items[i].Metric < report.Items[j].Metric
	})
	report.Total = len(report.Items)
	for _, discrepancy := range report.Items {
		if discrepancy.Decision == "" {
			report.PendingIDs = append(report.PendingIDs, discrepancy.ID)
		} else {
			report.Completed++
		}
	}
	if report.Total == 0 {
		report.CompletionPercentage = 100
	} else {
		report.CompletionPercentage = float64(report.Completed) * 100 / float64(report.Total)
	}
	return report
}

type ReleaseBlocker struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type ReleaseThresholdCheck struct {
	MetricCode string  `json:"metric_code"`
	Threshold  float64 `json:"threshold"`
	Passed     bool    `json:"passed"`
}

type ReleasePreview struct {
	CaseID                string                  `json:"case_id"`
	Revision              int                     `json:"revision"`
	Status                Status                  `json:"status"`
	CanSign               bool                    `json:"can_sign"`
	Thresholds            []ReleaseThresholdCheck `json:"thresholds"`
	OpenDiscrepancyIDs    []string                `json:"open_discrepancy_ids"`
	DutiesSeparated       bool                    `json:"duties_separated"`
	Blockers              []ReleaseBlocker        `json:"blockers"`
	ThresholdsSummaryHash string                  `json:"thresholds_summary_hash"`
	SnapshotHash          string                  `json:"snapshot_hash"`
	Remediation           *RemediationProgress    `json:"remediation,omitempty"`
}

func BuildReleasePreview(c *Case) ReleasePreview {
	p := ReleasePreview{CaseID: c.ID, Revision: c.Revision, Status: c.Status, Thresholds: []ReleaseThresholdCheck{}, OpenDiscrepancyIDs: []string{}, Blockers: []ReleaseBlocker{}}
	p.DutiesSeparated = c.Owner != "" && c.Reviewer != "" && c.Authorizer != "" && c.Owner != c.Reviewer && c.Owner != c.Authorizer && c.Reviewer != c.Authorizer
	if !p.DutiesSeparated {
		p.Blockers = append(p.Blockers, ReleaseBlocker{Code: "duty_conflict", Message: "职责分离检查未通过"})
	}
	if c.Status != ReviewPending && !(c.Status == Tested && len(c.Discrepancies) == 0) {
		p.Blockers = append(p.Blockers, ReleaseBlocker{Code: "invalid_state", Message: "当前状态不允许签署放行"})
	}
	for _, discrepancy := range c.Discrepancies {
		if discrepancy.Decision != "PASS" {
			p.OpenDiscrepancyIDs = append(p.OpenDiscrepancyIDs, discrepancy.ID)
		}
	}
	sort.Strings(p.OpenDiscrepancyIDs)
	if len(p.OpenDiscrepancyIDs) > 0 {
		p.Blockers = append(p.Blockers, ReleaseBlocker{Code: "open_discrepancies", Message: "仍有未通过的差异任务"})
	}
	if c.Plan == nil || len(c.Measurements) != 2 {
		p.Blockers = append(p.Blockers, ReleaseBlocker{Code: "measurements_incomplete", Message: "测量或阈值数据不完整"})
	} else {
		for _, rule := range DefaultMetricRules {
			passed := true
			threshold, ok := c.Plan.Thresholds[rule.Name]
			if !ok {
				passed = false
			}
			for _, measurement := range c.Measurements {
				_, itemPassed := thresholdMargin(rule.Name, measurementMetric(measurement, rule.Name), threshold)
				passed = passed && itemPassed
			}
			p.Thresholds = append(p.Thresholds, ReleaseThresholdCheck{MetricCode: rule.Name, Threshold: threshold, Passed: passed})
			if !passed {
				p.Blockers = append(p.Blockers, ReleaseBlocker{Code: "threshold_failed", Message: rule.Name + " 未达到用途阈值"})
			}
		}
	}
	thresholdBytes, _ := json.Marshal(p.Thresholds)
	thresholdHash := sha256.Sum256(thresholdBytes)
	p.ThresholdsSummaryHash = hex.EncodeToString(thresholdHash[:])
	p.CanSign = len(p.Blockers) == 0
	p.Remediation = BuildRemediationProgress(c, p.CanSign)
	snapshot := struct {
		CaseID                string                  `json:"case_id"`
		Revision              int                     `json:"revision"`
		Status                Status                  `json:"status"`
		Thresholds            []ReleaseThresholdCheck `json:"thresholds"`
		OpenDiscrepancyIDs    []string                `json:"open_discrepancy_ids"`
		DutiesSeparated       bool                    `json:"duties_separated"`
		ThresholdsSummaryHash string                  `json:"thresholds_summary_hash"`
	}{p.CaseID, p.Revision, p.Status, p.Thresholds, p.OpenDiscrepancyIDs, p.DutiesSeparated, p.ThresholdsSummaryHash}
	b, _ := json.Marshal(snapshot)
	h := sha256.Sum256(b)
	p.SnapshotHash = hex.EncodeToString(h[:])
	return p
}
