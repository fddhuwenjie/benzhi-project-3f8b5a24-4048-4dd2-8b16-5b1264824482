package domain

import (
	"errors"
	"sort"
	"strings"
	"time"
)

type RemediationProgress struct {
	Round                         int      `json:"round"`
	RejectedReleaseRevision       int      `json:"rejected_release_revision"`
	Reason                        string   `json:"reason"`
	FailedThresholds              []string `json:"failed_thresholds"`
	SigningSnapshotHash           string   `json:"signing_snapshot_hash"`
	SupersedesMeasurementRevision int      `json:"supersedes_measurement_revision"`
	ReplacementRevision           int      `json:"replacement_revision,omitempty"`
	RemediationNote               string   `json:"remediation_note,omitempty"`
	OpenDiscrepancyIDs            []string `json:"open_discrepancy_ids"`
	CanResign                     bool     `json:"can_resign"`
}

func (c *Case) latestMeasurementRevision() int {
	for i := len(c.Events) - 1; i >= 0; i-- {
		if c.Events[i].Type == "MEASUREMENTS_RECORDED" || c.Events[i].Type == "REMEDIATION_MEASUREMENTS_RECORDED" {
			return c.Events[i].Revision
		}
	}
	return 0
}

func (c *Case) ReplaceMeasurements(ms []Measurement, note string, supersedesRevision int, rid string) error {
	if c.Status != Tested || len(c.Remediations) == 0 {
		return errors.New("仅放行驳回形成的 TESTED 个案可提交整改测量")
	}
	roundIndex := len(c.Remediations) - 1
	round := c.Remediations[roundIndex]
	if round.ReplacementRevision != 0 {
		return errors.New("当前整改轮次已提交替换测量")
	}
	if strings.TrimSpace(note) == "" {
		return errors.New("整改说明不能为空")
	}
	currentRevision := c.latestMeasurementRevision()
	if supersedesRevision <= 0 || supersedesRevision != currentRevision || supersedesRevision != round.SupersedesMeasurementRevision {
		return errors.New("被替换 measurement revision 不匹配")
	}
	for _, m := range ms {
		if m.MeasuredBy == c.Reviewer || m.MeasuredBy == c.Authorizer {
			return errors.New("整改测量执行人与复核员、授权人职责必须分离")
		}
		if c.Plan == nil || !RequiredMetricsComplete(m, c.Plan.Metrics) {
			return errors.New("整改测量必测项不完整")
		}
	}
	// 在隔离副本上复用原双组测量的单位、范围、盲样组和差异校验。
	validated := CloneCase(c)
	validated.Status = Conditioned
	validated.Measurements = map[string]Measurement{}
	validated.Discrepancies = map[string]Discrepancy{}
	if err := validated.AddMeasurements(ms, "remediation-validation"); err != nil {
		return err
	}
	for _, rule := range DefaultMetricRules {
		threshold, ok := c.Plan.Thresholds[rule.Name]
		if !ok {
			return errors.New("整改测量缺少用途阈值")
		}
		for _, measurement := range validated.Measurements {
			if _, passed := thresholdMargin(rule.Name, measurementMetric(measurement, rule.Name), threshold); !passed {
				return errors.New("整改测量未通过用途阈值: " + rule.Name)
			}
		}
	}
	c.Measurements = validated.Measurements
	c.Discrepancies = validated.Discrepancies
	c.Status = Tested
	round.ReplacementRevision = c.Revision + 1
	round.RemediationNote = strings.TrimSpace(note)
	c.Remediations[roundIndex] = round
	replacements := make([]Measurement, 0, len(c.Measurements))
	groups := make([]string, 0, len(c.Measurements))
	for group := range c.Measurements {
		groups = append(groups, group)
	}
	sort.Strings(groups)
	for _, group := range groups {
		replacements = append(replacements, c.Measurements[group])
	}
	c.AppendEvent("REMEDIATION_MEASUREMENTS_RECORDED", map[string]any{
		"round": round.Round, "remediation_note": round.RemediationNote,
		"supersedes_revision": supersedesRevision, "measurements": replacements,
		"recorded_at": time.Now().UTC(),
	}, rid)
	return nil
}

func BuildRemediationProgress(c *Case, canResign bool) *RemediationProgress {
	if len(c.Remediations) == 0 {
		return nil
	}
	round := c.Remediations[len(c.Remediations)-1]
	progress := &RemediationProgress{
		Round: round.Round, RejectedReleaseRevision: round.RejectedReleaseRevision,
		Reason: round.Reason, FailedThresholds: append([]string(nil), round.FailedThresholds...),
		SigningSnapshotHash:           round.SigningSnapshotHash,
		SupersedesMeasurementRevision: round.SupersedesMeasurementRevision,
		ReplacementRevision:           round.ReplacementRevision, RemediationNote: round.RemediationNote,
		OpenDiscrepancyIDs: []string{}, CanResign: canResign,
	}
	for id, discrepancy := range c.Discrepancies {
		if discrepancy.Decision != "PASS" {
			progress.OpenDiscrepancyIDs = append(progress.OpenDiscrepancyIDs, id)
		}
	}
	sort.Strings(progress.OpenDiscrepancyIDs)
	return progress
}
