package domain

import (
	"fmt"
	"sort"
	"time"
)

type ConditioningBatchError struct {
	Index   int    `json:"index"`
	Field   string `json:"field"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *ConditioningBatchError) Error() string {
	return fmt.Sprintf("readings[%d].%s: %s", e.Index, e.Field, e.Message)
}

type ConditioningBatchResult struct {
	CaseID       string              `json:"case_id"`
	Revision     int                 `json:"revision"`
	Status       Status              `json:"status"`
	ValidateOnly bool                `json:"validate_only"`
	ReadingCount int                 `json:"reading_count"`
	Summary      ConditioningSummary `json:"summary"`
}

type indexedReading struct {
	reading ConditioningReading
	index   int
	isNew   bool
}

// PreviewConditioningBatch validates the complete merged timeline without mutating the case.
func PreviewConditioningBatch(c *Case, readings []ConditioningReading) (ConditioningBatchResult, []ConditioningReading, error) {
	if c.Status != PlanReady {
		return ConditioningBatchResult{}, nil, fmt.Errorf("状态不允许条件化")
	}
	if c.Plan == nil {
		return ConditioningBatchResult{}, nil, fmt.Errorf("条件化方案不存在")
	}
	if len(readings) == 0 {
		return ConditioningBatchResult{}, nil, fmt.Errorf("readings 不能为空")
	}
	for i, r := range readings {
		if r.At.IsZero() {
			return ConditioningBatchResult{}, nil, &ConditioningBatchError{Index: i, Field: "at", Code: "required", Message: "时间不能为空"}
		}
		if i > 0 && !r.At.After(readings[i-1].At) {
			code, message := "out_of_order", "时间必须严格递增"
			if r.At.Equal(readings[i-1].At) {
				code, message = "duplicate_timestamp", "时间戳重复"
			}
			return ConditioningBatchResult{}, nil, &ConditioningBatchError{Index: i, Field: "at", Code: code, Message: message}
		}
		if r.ExposedMinutes <= 0 {
			return ConditioningBatchResult{}, nil, &ConditioningBatchError{Index: i, Field: "exposed_minutes", Code: "out_of_range", Message: "暴露时长必须大于零"}
		}
		if !finite(r.Temperature) || r.Temperature < c.Plan.TempMin || r.Temperature > c.Plan.TempMax {
			return ConditioningBatchResult{}, nil, &ConditioningBatchError{Index: i, Field: "temperature", Code: "out_of_range", Message: "温度超出方案范围"}
		}
		if !finite(r.Humidity) || r.Humidity < c.Plan.HumMin || r.Humidity > c.Plan.HumMax {
			return ConditioningBatchResult{}, nil, &ConditioningBatchError{Index: i, Field: "humidity", Code: "out_of_range", Message: "湿度超出方案范围"}
		}
	}
	merged := make([]indexedReading, 0, len(c.Conditioning)+len(readings))
	for _, r := range c.Conditioning {
		merged = append(merged, indexedReading{reading: r, index: -1})
	}
	for i, r := range readings {
		merged = append(merged, indexedReading{reading: r, index: i, isNew: true})
	}
	sort.SliceStable(merged, func(i, j int) bool { return merged[i].reading.At.Before(merged[j].reading.At) })
	for i := 1; i < len(merged); i++ {
		previous, current := merged[i-1], merged[i]
		index := current.index
		if !current.isNew && previous.isNew {
			index = previous.index
		}
		if index < 0 {
			// 未修复的历史时间线问题仍使本次合并批次无效；下标 0 指向
			// 当前批次无法与既有时间线连成连续窗口的首个候选条目。
			index = 0
		}
		previousEnd := previous.reading.At.Add(time.Duration(previous.reading.ExposedMinutes) * time.Minute)
		switch {
		case current.reading.At.Equal(previous.reading.At):
			return ConditioningBatchResult{}, nil, &ConditioningBatchError{Index: index, Field: "at", Code: "duplicate_timestamp", Message: "时间戳与已有或批内读数重复"}
		case current.reading.At.Before(previousEnd):
			return ConditioningBatchResult{}, nil, &ConditioningBatchError{Index: index, Field: "at", Code: "overlap", Message: "读数时段与相邻读数重叠"}
		case current.reading.At.After(previousEnd):
			return ConditioningBatchResult{}, nil, &ConditioningBatchError{Index: index, Field: "at", Code: "gap", Message: "读数时段与相邻读数之间存在缺口"}
		}
	}
	combined := make([]ConditioningReading, len(merged))
	for i := range merged {
		combined[i] = merged[i].reading
	}
	preview := CloneCase(c)
	preview.Conditioning = combined
	summary := BuildConditioningSummary(preview)
	return ConditioningBatchResult{CaseID: c.ID, Revision: c.Revision, Status: c.Status, ValidateOnly: true, ReadingCount: len(combined), Summary: summary}, combined, nil
}

func (c *Case) AddConditioningBatch(readings []ConditioningReading, confirm bool, rid string) (ConditioningBatchResult, error) {
	result, combined, err := PreviewConditioningBatch(c, readings)
	if err != nil {
		return ConditioningBatchResult{}, err
	}
	if confirm && !result.Summary.Confirmable {
		return ConditioningBatchResult{}, fmt.Errorf("最长连续合规窗口未达到方案最小暴露时长")
	}
	c.Conditioning = combined
	c.AppendEvent("CONDITIONING_BATCH_RECORDED", map[string]any{"readings": readings, "merged_reading_count": len(combined)}, rid)
	if confirm {
		window := result.Summary.LongestWindow
		c.Status = Conditioned
		c.AppendEvent("CONDITIONED", map[string]any{"window_from": window.Start, "window_to": window.End, "total_minutes": window.Minutes}, rid)
	}
	result.Revision = c.Revision
	result.Status = c.Status
	result.ValidateOnly = false
	return result, nil
}
