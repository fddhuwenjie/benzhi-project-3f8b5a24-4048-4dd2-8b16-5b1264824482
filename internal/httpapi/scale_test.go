package httpapi

import (
	"bytes"
	"encoding/json"
	"guji-paper/internal/application"
	"guji-paper/internal/store"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type apiHarness struct {
	t       *testing.T
	handler http.Handler
}

func (h apiHarness) request(method, path string, body any) (int, map[string]any) {
	h.t.Helper()
	data, _ := json.Marshal(body)
	req := httptest.NewRequest(method, path, bytes.NewReader(data))
	response := httptest.NewRecorder()
	h.handler.ServeHTTP(response, req)
	var value map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &value); err != nil {
		h.t.Fatalf("%s %s 返回无效 JSON: %v", method, path, err)
	}
	return response.Code, value
}

func (h apiHarness) post(path string, body any) map[string]any {
	h.t.Helper()
	code, value := h.request(http.MethodPost, path, body)
	if code >= 300 {
		h.t.Fatalf("POST %s = %d: %v", path, code, value)
	}
	return value
}

func (h apiHarness) get(path string) map[string]any {
	h.t.Helper()
	code, value := h.request(http.MethodGet, path, nil)
	if code != http.StatusOK {
		h.t.Fatalf("GET %s = %d: %v", path, code, value)
	}
	return value
}

func revisionOf(value map[string]any) int { return int(value["revision"].(float64)) }

func newPlannedCase(t *testing.T, h apiHarness, id string, minExposure int, colorThreshold float64) map[string]any {
	t.Helper()
	c := h.post(APIPrefix, map[string]any{"request_id": id + "-create", "case_id": id, "batch": "batch", "material": "古籍修复纸", "purpose": "修补", "owner": "owner", "reviewer": "reviewer", "authorizer": "authorizer"})
	return h.post(APIPrefix+"/"+id+"/plan", map[string]any{
		"request_id": id + "-plan", "expected_revision": revisionOf(c), "groups": []string{"A", "B"},
		"temp_min": 18, "temp_max": 24, "hum_min": 45, "hum_max": 60, "min_exposure": minExposure,
		"metrics":    []string{"tensile", "ph", "color_delta_e", "fiber_change"},
		"thresholds": map[string]float64{"tensile": 5, "ph": 7, "color_delta_e": colorThreshold, "fiber_change": 3}, "submitted_by": "planner",
	})
}

func TestConditioningBatchAtomicValidationAndRestartIdempotency(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	h := apiHarness{t: t, handler: New(application.New(st)).Handler()}
	c := newPlannedCase(t, h, "batch-case", 40, 3)
	start := time.Date(2026, 8, 26, 9, 0, 0, 0, time.UTC)
	c = h.post(APIPrefix+"/batch-case/conditioning", readingBody("batch-history", revisionOf(c), start, 10))
	before := revisionOf(c)
	readings := []any{
		map[string]any{"temperature": 21, "humidity": 50, "exposed_minutes": 10, "at": start.Add(10 * time.Minute)},
		map[string]any{"temperature": 21, "humidity": 50, "exposed_minutes": 10, "at": start.Add(20 * time.Minute)},
		map[string]any{"temperature": 21, "humidity": 50, "exposed_minutes": 10, "at": start.Add(30 * time.Minute)},
	}
	preview := h.post(APIPrefix+"/batch-case/conditioning", map[string]any{"request_id": "batch-preview", "expected_revision": before, "validate_only": true, "confirm": true, "readings": readings})
	summary := preview["summary"].(map[string]any)
	if preview["validate_only"] != true || summary["confirmable"] != true || int(summary["cumulative_exposure_minutes"].(float64)) != 40 {
		t.Fatalf("批量预检摘要错误: %v", preview)
	}
	if got := revisionOf(h.get(APIPrefix + "/batch-case")); got != before {
		t.Fatalf("预检推进了 revision: %d -> %d", before, got)
	}
	bad := cloneSlice(readings)
	bad[1].(map[string]any)["temperature"] = 30
	code, problem := h.request(http.MethodPost, APIPrefix+"/batch-case/conditioning", map[string]any{"request_id": "batch-bad", "expected_revision": before, "readings": bad})
	if code != http.StatusConflict || int(problem["index"].(float64)) != 1 {
		t.Fatalf("批量错误未绑定数组下标: %d %v", code, problem)
	}
	if got := revisionOf(h.get(APIPrefix + "/batch-case")); got != before {
		t.Fatalf("失败批次改变了 revision: %d -> %d", before, got)
	}
	body := map[string]any{"request_id": "batch-commit", "expected_revision": before, "confirm": true, "readings": readings}
	c = h.post(APIPrefix+"/batch-case/conditioning", body)
	if c["status"] != "CONDITIONED" || len(c["conditioning"].([]any)) != 4 {
		t.Fatalf("正式批次未原子确认: %v", c)
	}
	firstRevision := revisionOf(c)
	h.post(APIPrefix+"/batch-case/measurements", map[string]any{"request_id": "after-batch-measurements", "expected_revision": firstRevision, "measurements": []any{
		map[string]any{"id": "batch-a", "group": "A", "measured_by": "technician", "tensile": 10, "ph_value": 6, "color_delta_e": 1, "fiber_dimension_change_rate": 1},
		map[string]any{"id": "batch-b", "group": "B", "measured_by": "technician", "tensile": 10, "ph_value": 6, "color_delta_e": 1, "fiber_dimension_change_rate": 1},
	}})
	reopened, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	h.handler = New(application.New(reopened)).Handler()
	retried := h.post(APIPrefix+"/batch-case/conditioning", body)
	if revisionOf(retried) != firstRevision || retried["status"] != "CONDITIONED" {
		t.Fatalf("重启幂等结果不一致: %v", retried)
	}
	changed := cloneMap(body)
	changedReadings := cloneSlice(readings)
	changedReadings[2].(map[string]any)["humidity"] = 51
	changed["readings"] = changedReadings
	if code, _ := h.request(http.MethodPost, APIPrefix+"/batch-case/conditioning", changed); code != http.StatusConflict {
		t.Fatalf("不同批量载荷复用 request_id 应冲突，得到 %d", code)
	}
}

func TestReleaseRemediationAndSealedEvidenceCredential(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	h := apiHarness{t: t, handler: New(application.New(st)).Handler()}
	c := newPlannedCase(t, h, "remediation-case", 20, 1)
	start := time.Date(2026, 8, 26, 11, 0, 0, 0, time.UTC)
	c = h.post(APIPrefix+"/remediation-case/conditioning", map[string]any{"request_id": "remediation-conditioning", "expected_revision": revisionOf(c), "confirm": true, "readings": []any{
		map[string]any{"temperature": 21, "humidity": 50, "exposed_minutes": 10, "at": start},
		map[string]any{"temperature": 21, "humidity": 50, "exposed_minutes": 10, "at": start.Add(10 * time.Minute)},
	}})
	initial := []any{
		map[string]any{"id": "old-a", "group": "A", "measured_by": "technician", "tensile": 10, "ph_value": 6, "color_delta_e": 2, "fiber_dimension_change_rate": 1},
		map[string]any{"id": "old-b", "group": "B", "measured_by": "technician", "tensile": 10, "ph_value": 6, "color_delta_e": 2, "fiber_dimension_change_rate": 1},
	}
	c = h.post(APIPrefix+"/remediation-case/measurements", map[string]any{"request_id": "old-measurements", "expected_revision": revisionOf(c), "measurements": initial})
	measurementRevision := revisionOf(c)
	preview := h.get(APIPrefix + "/remediation-case/release")
	oldSnapshot := preview["snapshot_hash"]
	c = h.post(APIPrefix+"/remediation-case/release", map[string]any{"request_id": "reject-release", "expected_revision": revisionOf(c), "approved": false, "reason": "色差未达到用途阈值", "by": "authorizer", "snapshot_hash": oldSnapshot})
	progress := h.get(APIPrefix + "/remediation-case/release")
	remediation := progress["remediation"].(map[string]any)
	if c["status"] != "TESTED" || int(remediation["round"].(float64)) != 1 || int(remediation["supersedes_measurement_revision"].(float64)) != measurementRevision {
		t.Fatalf("驳回整改轮次错误: %v", progress)
	}
	replacements := []any{
		map[string]any{"id": "new-a", "group": "A", "measured_by": "technician", "tensile": 10, "ph_value": 6, "color_delta_e": .4, "fiber_dimension_change_rate": 1},
		map[string]any{"id": "new-b", "group": "B", "measured_by": "technician", "tensile": 10, "ph_value": 6, "color_delta_e": .6, "fiber_dimension_change_rate": 1},
	}
	beforeReplacement := revisionOf(c)
	if code, _ := h.request(http.MethodPost, APIPrefix+"/remediation-case/measurements", map[string]any{"request_id": "bad-replacement", "expected_revision": beforeReplacement, "measurements": replacements, "remediation_note": "重新校准仪器", "supersedes_revision": measurementRevision - 1}); code != http.StatusConflict {
		t.Fatalf("错误旧测量 revision 应冲突，得到 %d", code)
	}
	if got := revisionOf(h.get(APIPrefix + "/remediation-case")); got != beforeReplacement {
		t.Fatalf("失败整改覆盖了当前测量: %d -> %d", beforeReplacement, got)
	}
	c = h.post(APIPrefix+"/remediation-case/measurements", map[string]any{"request_id": "replacement", "expected_revision": beforeReplacement, "measurements": replacements, "remediation_note": "重新校准仪器并复测", "supersedes_revision": measurementRevision})
	replacementRevision := revisionOf(c)
	items := h.get(APIPrefix + "/remediation-case/discrepancies")["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("整改后差异集合未重建: %v", items)
	}
	item := items[0].(map[string]any)
	c = h.post(APIPrefix+"/remediation-case/discrepancies", map[string]any{"request_id": "replacement-review", "expected_revision": revisionOf(c), "id": item["id"], "metric": item["metric"], "reason": "复测结果稳定", "decision": "PASS", "reviewer": "reviewer", "retest": []float64{.5}})
	newPreview := h.get(APIPrefix + "/remediation-case/release")
	if newPreview["can_sign"] != true || newPreview["snapshot_hash"] == oldSnapshot {
		t.Fatalf("整改后未生成新的可签署快照: %v", newPreview)
	}
	if code, _ := h.request(http.MethodPost, APIPrefix+"/remediation-case/release", map[string]any{"request_id": "stale-sign", "expected_revision": revisionOf(c), "approved": true, "reason": "旧快照", "by": "authorizer", "snapshot_hash": oldSnapshot}); code != http.StatusConflict {
		t.Fatalf("旧签署快照应冲突，得到 %d", code)
	}
	c = h.post(APIPrefix+"/remediation-case/release", map[string]any{"request_id": "approve-release", "expected_revision": revisionOf(c), "approved": true, "reason": "整改与复核均通过", "by": "authorizer", "snapshot_hash": newPreview["snapshot_hash"]})
	h.post(APIPrefix+"/remediation-case/seal", map[string]any{"request_id": "seal-remediation", "expected_revision": revisionOf(c), "by": "authorizer"})
	credential := h.get(APIPrefix + "/remediation-case/manifest?evidence_stage=measurement&from_revision=" + itoa(replacementRevision) + "&to_revision=" + itoa(replacementRevision) + "&page=1&page_size=1")
	if credential["manifest_verified"] != true || credential["event_chain_verified"] != true || credential["total"] != float64(1) {
		t.Fatalf("测量证据选择错误: %v", credential)
	}
	selectionHash := credential["selection_sha256"]
	manifestHash := credential["manifest_sha256"]
	root := credential["event_chain_root"]
	restarted, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	h.handler = New(application.New(restarted)).Handler()
	verified := h.get(APIPrefix + "/remediation-case/manifest?evidence_stage=measurement&from_revision=" + itoa(replacementRevision) + "&to_revision=" + itoa(replacementRevision) + "&page=1&page_size=20&expected_manifest_sha256=" + manifestHash.(string) + "&expected_event_chain_root=" + root.(string) + "&expected_selection_sha256=" + selectionHash.(string))
	if verified["selection_sha256"] != selectionHash || verified["manifest_sha256_match"] != true || verified["event_chain_root_match"] != true || verified["selection_sha256_match"] != true {
		t.Fatalf("重启或分页改变了核验凭据: %v", verified)
	}
	mismatch := h.get(APIPrefix + "/remediation-case/manifest?evidence_stage=measurement&from_revision=" + itoa(replacementRevision) + "&to_revision=" + itoa(replacementRevision) + "&expected_selection_sha256=deadbeef")
	if mismatch["selection_sha256_match"] != false {
		t.Fatalf("错误选择摘要未明确报告: %v", mismatch)
	}
	if code, value := h.request(http.MethodGet, APIPrefix+"/remediation-case/manifest?evidence_stage=unknown", nil); code != http.StatusBadRequest || value["items"] != nil {
		t.Fatalf("未知阶段应拒绝且不返回条目: %d %v", code, value)
	}
}

func itoa(value int) string {
	const digits = "0123456789"
	if value == 0 {
		return "0"
	}
	result := make([]byte, 0, 10)
	for value > 0 {
		result = append([]byte{digits[value%10]}, result...)
		value /= 10
	}
	return string(result)
}
