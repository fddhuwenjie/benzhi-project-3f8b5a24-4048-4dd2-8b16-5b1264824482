package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"guji-paper/internal/application"
	"guji-paper/internal/store"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestExtendedQualificationFlowAndRestart(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	handler := New(application.New(st)).Handler()
	request := func(method, path string, body any) (int, map[string]any) {
		t.Helper()
		var data []byte
		if body != nil {
			data, _ = json.Marshal(body)
		}
		req := httptest.NewRequest(method, path, bytes.NewReader(data))
		resp := httptest.NewRecorder()
		handler.ServeHTTP(resp, req)
		var value map[string]any
		if err := json.Unmarshal(resp.Body.Bytes(), &value); err != nil {
			t.Fatalf("%s %s 返回无效 JSON: %v", method, path, err)
		}
		return resp.Code, value
	}
	post := func(path string, body any) map[string]any {
		t.Helper()
		code, value := request(http.MethodPost, path, body)
		if code >= 300 {
			t.Fatalf("POST %s = %d: %v", path, code, value)
		}
		return value
	}
	get := func(path string) map[string]any {
		t.Helper()
		code, value := request(http.MethodGet, path, nil)
		if code != http.StatusOK {
			t.Fatalf("GET %s = %d: %v", path, code, value)
		}
		return value
	}
	revision := func(value map[string]any) int { return int(value["revision"].(float64)) }

	caseValue := post(APIPrefix, map[string]any{"request_id": "create-1", "case_id": "case-1", "batch": "paper-7", "material": "古籍竹纸", "purpose": "修补", "owner": "owner", "reviewer": "reviewer", "authorizer": "authorizer"})
	invalidPlan := map[string]any{"request_id": "validate-1", "expected_revision": revision(caseValue), "validate_only": true, "groups": []string{"B", "A"}, "temp_min": 18, "temp_max": 24, "hum_min": 45, "hum_max": 60, "min_exposure": 60, "metrics": []string{"tensile", "ph", "color_delta_e"}, "thresholds": map[string]float64{"tensile": 5, "ph": 7, "color_delta_e": 101}, "submitted_by": "planner"}
	validation := post(APIPrefix+"/case-1/plan", invalidPlan)
	if validation["valid"] != false || len(validation["errors"].([]any)) < 2 {
		t.Fatalf("预校验未同时报告缺项与越界: %v", validation)
	}
	if got := revision(get(APIPrefix + "/case-1")); got != 1 {
		t.Fatalf("预校验改变了 revision: %d", got)
	}
	post(APIPrefix+"/case-1/plan", invalidPlan)
	changed := cloneMap(invalidPlan)
	changed["min_exposure"] = 90
	if code, _ := request(http.MethodPost, APIPrefix+"/case-1/plan", changed); code != http.StatusConflict {
		t.Fatalf("不同载荷复用预校验 request_id 应冲突，得到 %d", code)
	}

	planBody := map[string]any{"request_id": "plan-1", "expected_revision": 1, "groups": []string{"B", "A"}, "temp_min": 18, "temp_max": 24, "hum_min": 45, "hum_max": 60, "min_exposure": 60, "metrics": []string{"fiber_change", "color_delta_e", "ph", "tensile"}, "thresholds": map[string]float64{"tensile": 5, "ph": 7, "color_delta_e": 3, "fiber_change": 3}, "submitted_by": "planner"}
	caseValue = post(APIPrefix+"/case-1/plan", planBody)
	start := time.Date(2026, 8, 26, 10, 0, 0, 0, time.UTC)
	caseValue = post(APIPrefix+"/case-1/conditioning", readingBody("reading-1", revision(caseValue), start, 30))
	caseValue = post(APIPrefix+"/case-1/conditioning", readingBody("reading-2", revision(caseValue), start.Add(40*time.Minute), 30))
	summary := get(APIPrefix + "/case-1/conditioning/summary")
	issue := summary["first_issue"].(map[string]any)
	if issue["kind"] != "存在缺口" || int(summary["longest_continuous_minutes"].(float64)) != 30 {
		t.Fatalf("缺口摘要错误: %v", summary)
	}
	if code, _ := request(http.MethodPost, APIPrefix+"/case-1/conditioning", map[string]any{"request_id": "confirm-bad", "expected_revision": revision(caseValue), "confirm": true}); code != http.StatusConflict {
		t.Fatalf("存在缺口时确认应冲突，得到 %d", code)
	}
	caseValue = post(APIPrefix+"/case-1/conditioning", readingBody("reading-fill", revision(caseValue), start.Add(30*time.Minute), 10))
	summary = get(APIPrefix + "/case-1/conditioning/summary")
	window := summary["longest_window"].(map[string]any)
	if int(summary["longest_continuous_minutes"].(float64)) != 70 {
		t.Fatalf("补录后的连续窗口错误: %v", summary)
	}
	caseValue = post(APIPrefix+"/case-1/conditioning", map[string]any{"request_id": "confirm-1", "expected_revision": revision(caseValue), "confirm": true, "window_from": window["start"], "window_to": window["end"]})

	measurements := []any{
		map[string]any{"id": "m-a", "group": "A", "measured_by": "technician", "tensile": 0.01, "ph_value": 6, "color_delta_e": 1, "fiber_dimension_change_rate": 1, "units": map[string]string{"tensile": "kN"}},
		map[string]any{"id": "m-b", "group": "B", "measured_by": "technician", "tensile": 12, "ph_value": 6.5, "color_delta_e": 2, "fiber_dimension_change_rate": 1.5, "units": map[string]string{"tensile": "N"}},
	}
	caseValue = post(APIPrefix+"/case-1/measurements", map[string]any{"request_id": "measure-1", "expected_revision": revision(caseValue), "measurements": measurements})
	report := get(APIPrefix + "/case-1/measurements/report")
	if report["verified"] != true {
		t.Fatalf("测量报告未通过事件绑定核验: %v", report)
	}
	metrics := report["metrics"].([]any)
	var tensile map[string]any
	for _, item := range metrics {
		metric := item.(map[string]any)
		if metric["metric_code"] == "tensile" {
			tensile = metric
		}
	}
	values := tensile["values"].([]any)
	if values[0].(map[string]any)["value"] != float64(10) || values[0].(map[string]any)["raw_unit"] != "kN" {
		t.Fatalf("kN 未正确归一化: %v", tensile)
	}

	progress := get(APIPrefix + "/case-1/discrepancies")
	items := progress["items"].([]any)
	if len(items) != 4 || progress["completion_percentage"] != float64(0) {
		t.Fatalf("差异任务初始化错误: %v", progress)
	}
	decisions := make([]any, 0, len(items))
	for _, raw := range items {
		item := raw.(map[string]any)
		decisions = append(decisions, map[string]any{"id": item["id"], "metric": item["metric"], "reason": "复测稳定", "decision": "PASS", "reviewer": "reviewer", "retest": []float64{1}})
	}
	bad := cloneSlice(decisions)
	bad[1].(map[string]any)["metric"] = "unknown"
	beforeBatch := revision(caseValue)
	if code, _ := request(http.MethodPost, APIPrefix+"/case-1/discrepancies", map[string]any{"request_id": "batch-bad", "expected_revision": beforeBatch, "items": bad}); code != http.StatusConflict {
		t.Fatalf("无效批量裁决应冲突，得到 %d", code)
	}
	if got := revision(get(APIPrefix + "/case-1")); got != beforeBatch {
		t.Fatalf("失败批量裁决改变 revision: %d -> %d", beforeBatch, got)
	}
	caseValue = post(APIPrefix+"/case-1/discrepancies", map[string]any{"request_id": "batch-half", "expected_revision": beforeBatch, "items": decisions[:2]})
	progress = get(APIPrefix + "/case-1/discrepancies")
	if progress["completion_percentage"] != float64(50) || progress["status"] != "TESTED" {
		t.Fatalf("部分完成度错误: %v", progress)
	}
	caseValue = post(APIPrefix+"/case-1/discrepancies", map[string]any{"request_id": "batch-rest", "expected_revision": revision(caseValue), "items": decisions[2:]})
	preview := get(APIPrefix + "/case-1/release")
	if preview["can_sign"] != true {
		t.Fatalf("放行预览存在非预期阻断: %v", preview)
	}
	caseValue = post(APIPrefix+"/case-1/release", map[string]any{"request_id": "release-1", "expected_revision": revision(caseValue), "approved": true, "reason": "符合修补用途", "by": "authorizer", "snapshot_hash": preview["snapshot_hash"]})
	post(APIPrefix+"/case-1/seal", map[string]any{"request_id": "seal-1", "expected_revision": revision(caseValue), "by": "authorizer"})
	manifest := get(APIPrefix + "/case-1/manifest?page=1&page_size=3&include_entries=true")
	if manifest["verified"] != true || len(manifest["entries"].([]any)) != 3 {
		t.Fatalf("封存分页核验错误: %v", manifest)
	}
	auditPage := get(APIPrefix + "/case-1/audit?event_type=MEASUREMENTS_RECORDED&page=1&page_size=1")
	if auditPage["verified"] != true || auditPage["total"] != float64(1) || auditPage["range_first_hash"] == "" || auditPage["range_last_hash"] == "" {
		t.Fatalf("审计区间证明错误: %v", auditPage)
	}

	reopened, err := store.Open(dir)
	if err != nil {
		t.Fatalf("重启回放失败: %v", err)
	}
	handler = New(application.New(reopened)).Handler()
	restartedManifest := get(APIPrefix + "/case-1/manifest?page=1&page_size=3&include_entries=true")
	if restartedManifest["manifest_sha256"] != manifest["manifest_sha256"] || restartedManifest["verified"] != true {
		t.Fatalf("重启后清单不一致: %v", restartedManifest)
	}
	from := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	to := time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
	listed := get(fmt.Sprintf("%s?created_from=%s&created_to=%s&status=SEALED&page=1&page_size=1", APIPrefix, from, to))
	if listed["total"] != float64(1) || len(listed["cases"].([]any)) != 1 {
		t.Fatalf("重启后的时段列表错误: %v", listed)
	}
}

func readingBody(requestID string, revision int, at time.Time, minutes int) map[string]any {
	return map[string]any{"request_id": requestID, "expected_revision": revision, "temperature": 21, "humidity": 50, "exposed_minutes": minutes, "at": at.Format(time.RFC3339)}
}

func cloneMap(value map[string]any) map[string]any {
	b, _ := json.Marshal(value)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return out
}

func cloneSlice(value []any) []any {
	b, _ := json.Marshal(value)
	var out []any
	_ = json.Unmarshal(b, &out)
	return out
}
