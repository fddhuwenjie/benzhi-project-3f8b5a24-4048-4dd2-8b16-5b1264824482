package domain

import "strings"

func EvidenceStageForEvent(eventType string) (string, bool) {
	switch eventType {
	case "CASE_CREATED":
		return "filing", true
	case "PLAN_SUBMITTED":
		return "plan", true
	case "CONDITIONING_READING", "CONDITIONING_BATCH_RECORDED", "CONDITIONED":
		return "conditioning", true
	case "MEASUREMENTS_RECORDED", "REMEDIATION_MEASUREMENTS_RECORDED":
		return "measurement", true
	case "DISCREPANCY_DECIDED", "REVIEW_RESULT":
		return "retest", true
	case "RELEASE_DECISION":
		return "release", true
	case "SEALED":
		return "seal", true
	default:
		return "", false
	}
}

func ValidEvidenceStage(stage string) bool {
	_, ok := NormalizeEvidenceStage(stage)
	return ok
}

func NormalizeEvidenceStage(stage string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(stage)) {
	case "filing", "case", "建档":
		return "filing", true
	case "plan", "方案":
		return "plan", true
	case "conditioning", "条件化":
		return "conditioning", true
	case "measurement", "measurements", "测量":
		return "measurement", true
	case "retest", "复测":
		return "retest", true
	case "release", "放行":
		return "release", true
	case "seal", "封存":
		return "seal", true
	default:
		return "", false
	}
}
