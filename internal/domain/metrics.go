package domain

import (
	"fmt"
	"math"
)

type MetricRule struct {
	Name     string
	Min, Max float64
	Unit     string
}

var DefaultMetricRules = []MetricRule{{"tensile", 0, 1000, "N"}, {"ph", 0, 14, "pH"}, {"color_delta_e", 0, 100, "ΔE"}, {"fiber_change", 0, 100, "%"}}

func MetricNames() []string {
	r := make([]string, len(DefaultMetricRules))
	for i, x := range DefaultMetricRules {
		r[i] = x.Name
	}
	return r
}
func ValidateMetric(name string, v float64) error {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return fmt.Errorf("%s 必须为有限数值", name)
	}
	for _, r := range DefaultMetricRules {
		if r.Name == name {
			if v < r.Min || v > r.Max {
				return fmt.Errorf("%s 超出范围", name)
			}
			return nil
		}
	}
	return fmt.Errorf("未知指标 %s", name)
}
func CompareMetrics(a, b Measurement) map[string]float64 {
	return map[string]float64{"tensile": abs(a.Tensile - b.Tensile), "ph": abs(a.PH - b.PH), "color_delta_e": abs(a.ColorDelta - b.ColorDelta), "fiber_change": abs(a.FiberChange - b.FiberChange)}
}
func RequiredMetricsComplete(m Measurement, names []string) bool {
	for _, n := range names {
		switch n {
		case "tensile":
			if m.Tensile <= 0 {
				return false
			}
		case "ph":
			if m.PH <= 0 {
				return false
			}
		case "color_delta_e":
			if m.ColorDelta < 0 {
				return false
			}
		case "fiber_change":
			if m.FiberChange < 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}
