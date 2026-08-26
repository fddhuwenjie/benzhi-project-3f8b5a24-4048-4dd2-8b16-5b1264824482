package httpapi

import (
	"encoding/json"
	"guji-paper/internal/application"
	"guji-paper/internal/domain"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type Server struct {
	app *application.Service
	mux *http.ServeMux
}

func New(app *application.Service) *Server {
	s := &Server{app: app, mux: http.NewServeMux()}
	s.routes()
	return s
}
func (s *Server) routes() {
	s.mux.HandleFunc("/healthz", s.health)
	s.mux.HandleFunc("/readyz", s.health)
	s.mux.HandleFunc("/api/v1/qualification-cases", s.cases)
	s.mux.HandleFunc("/api/v1/qualification-cases/", s.caseSub)
}
func (s *Server) Handler() http.Handler { return s.mux }
func write(w http.ResponseWriter, v any, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
func decode(w *http.Request, v any) error {
	r := io.LimitReader(w.Body, 1<<20)
	d := json.NewDecoder(r)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		if err == nil {
			return &json.SyntaxError{}
		}
		return err
	}
	return nil
}
func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	write(w, map[string]string{"status": "ok"}, 200)
}

type createReq struct {
	RequestID  string `json:"request_id"`
	CaseID     string `json:"case_id"`
	Batch      string `json:"batch"`
	Material   string `json:"material"`
	Purpose    string `json:"purpose"`
	Owner      string `json:"owner"`
	Reviewer   string `json:"reviewer"`
	Authorizer string `json:"authorizer"`
}

func (s *Server) cases(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		page, size := 1, 20
		var err error
		if v := r.URL.Query().Get("page"); v != "" {
			page, err = strconv.Atoi(v)
			if err != nil {
				write(w, Problem{Error: "invalid page"}, 400)
				return
			}
		}
		if v := r.URL.Query().Get("page_size"); v != "" {
			size, err = strconv.Atoi(v)
			if err != nil {
				write(w, Problem{Error: "invalid page_size"}, 400)
				return
			}
		}
		q := r.URL.Query()
		var createdFrom, createdTo *time.Time
		for name, target := range map[string]**time.Time{"created_from": &createdFrom, "created_to": &createdTo} {
			if value := q.Get(name); value != "" {
				parsed, parseErr := time.Parse(time.RFC3339, value)
				if parseErr != nil {
					write(w, Problem{Error: name + " 必须为 RFC3339 时间"}, 400)
					return
				}
				utc := parsed.UTC()
				*target = &utc
			}
		}
		staleMinutes := 0
		if value := q.Get("stale_minutes"); value != "" {
			staleMinutes, err = strconv.Atoi(value)
			if err != nil || staleMinutes < 0 {
				write(w, Problem{Error: "stale_minutes 必须为非负整数"}, 400)
				return
			}
		}
		out, e := s.app.ListCases(application.CaseListFilter{Status: q.Get("status"), Purpose: q.Get("purpose"), Owner: q.Get("owner_id"), Batch: q.Get("candidate_batch_code"), CreatedFrom: createdFrom, CreatedTo: createdTo, StaleMinutes: staleMinutes, Page: page, PageSize: size})
		if e != nil {
			write(w, Problem{Error: e.Error()}, 400)
			return
		}
		write(w, out, 200)
		return
	}
	if r.Method != "POST" {
		write(w, map[string]string{"error": "method"}, 405)
		return
	}
	var q createReq
	if decode(r, &q) != nil || q.RequestID == "" {
		write(w, map[string]string{"error": "invalid request"}, 400)
		return
	}
	c, e := domain.NewCase(q.CaseID, q.Batch, q.Material, q.Purpose, q.Owner, q.Reviewer, q.Authorizer)
	if e != nil {
		write(w, map[string]string{"error": e.Error()}, 400)
		return
	}
	c, e = s.app.Create(c, q.RequestID)
	if e != nil {
		write(w, map[string]string{"error": e.Error()}, 409)
		return
	}
	write(w, c, 201)
}
func (s *Server) caseSub(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 4 {
		write(w, map[string]string{"error": "path"}, 404)
		return
	}
	id := parts[3]
	if len(parts) == 4 && r.Method == "GET" {
		if c := s.app.Get(id); c != nil {
			if c.Status == domain.Sealed {
				m, _ := s.app.Manifest(id)
				write(w, map[string]any{"case_id": c.ID, "status": c.Status, "revision": c.Revision, "created_at": c.CreatedAt, "sealed_at": c.SealedAt, "manifest": map[string]any{"manifest_id": m.ManifestID, "manifest_sha256": m.ManifestSHA256, "event_chain_root": m.EventChainRoot, "verified": m.Verified}}, 200)
			} else {
				write(w, c, 200)
			}
		} else {
			write(w, map[string]string{"error": "not found"}, 404)
		}
		return
	}
	if len(parts) == 5 && parts[4] == "manifest" && r.Method == "GET" {
		page, size, err := pagination(r, 1, 20)
		if err != nil {
			write(w, Problem{Error: err.Error()}, 400)
			return
		}
		query := r.URL.Query()
		if query.Get("evidence_stage") != "" {
			from, to := 1, 0
			if value := query.Get("from_revision"); value != "" {
				from, err = strconv.Atoi(value)
			}
			if err == nil && query.Get("to_revision") != "" {
				to, err = strconv.Atoi(query.Get("to_revision"))
			}
			if err != nil || from < 1 || to > 0 && to < from {
				write(w, Problem{Error: "修订区间无效"}, 400)
				return
			}
			if to == 0 {
				if c := s.app.Get(id); c != nil {
					to = c.Revision
				}
			}
			out, appErr := s.app.EvidenceCredential(id, application.EvidenceQuery{
				Stage: query.Get("evidence_stage"), FromRevision: from, ToRevision: to,
				Page: page, PageSize: size,
				ExpectedManifestSHA256:  firstNonEmpty(query.Get("expected_manifest_sha256"), query.Get("manifest_sha256")),
				ExpectedEventChainRoot:  firstNonEmpty(query.Get("expected_event_chain_root"), query.Get("event_chain_root")),
				ExpectedSelectionSHA256: firstNonEmpty(query.Get("expected_selection_sha256"), query.Get("selection_sha256")),
			})
			if appErr != nil {
				if out.FirstInvalidRevision > 0 || out.ManifestSHA256 != "" && !out.ManifestVerified {
					write(w, out, http.StatusConflict)
				} else {
					write(w, Problem{Error: appErr.Error()}, statusFor(appErr))
				}
				return
			}
			w.Header().Set("Cache-Control", "private, immutable")
			write(w, out, 200)
			return
		}
		includeEntries := false
		if value := r.URL.Query().Get("include_entries"); value != "" {
			includeEntries, err = strconv.ParseBool(value)
			if err != nil {
				write(w, Problem{Error: "include_entries 必须为布尔值"}, 400)
				return
			}
		}
		manifest, appErr := s.app.ManifestPage(id, page, size, includeEntries)
		if appErr != nil {
			write(w, Problem{Error: appErr.Error()}, statusFor(appErr))
			return
		}
		w.Header().Set("Cache-Control", "private, immutable")
		w.Header().Set("ETag", `"`+manifest.ManifestSHA256+`"`)
		write(w, manifest, 200)
		return
	}
	if len(parts) == 5 && parts[4] == "audit" && r.Method == "GET" {
		q := r.URL.Query()
		page, size, err := pagination(r, 1, 20)
		if err != nil {
			write(w, Problem{Error: err.Error()}, 400)
			return
		}
		from, to := 1, 0
		if q.Get("from_revision") != "" {
			from, err = strconv.Atoi(q.Get("from_revision"))
		}
		if err == nil && q.Get("to_revision") != "" {
			to, err = strconv.Atoi(q.Get("to_revision"))
		}
		if err != nil || from < 1 || to > 0 && to < from {
			write(w, Problem{Error: "修订区间无效"}, 400)
			return
		}
		if eventType := q.Get("event_type"); eventType != "" && !allowedEventType(eventType) {
			write(w, Problem{Error: "未知 event_type"}, 400)
			return
		}
		out, appErr := s.app.Audit(id, application.AuditQuery{EventType: q.Get("event_type"), RequestID: q.Get("request_id"), FromRevision: from, ToRevision: to, Page: page, PageSize: size})
		if appErr != nil {
			code := statusFor(appErr)
			if out.Error != "" {
				code = 409
				write(w, out, code)
			} else {
				write(w, Problem{Error: appErr.Error()}, code)
			}
			return
		}
		write(w, out, 200)
		return
	}
	if len(parts) == 5 && parts[4] == "discrepancies" && r.Method == "GET" {
		out, err := s.app.DiscrepancyProgress(id)
		if err != nil {
			write(w, Problem{Error: err.Error()}, statusFor(err))
			return
		}
		write(w, out, 200)
		return
	}
	if len(parts) == 5 && parts[4] == "release" && r.Method == "GET" {
		out, err := s.app.ReleasePreview(id)
		if err != nil {
			write(w, Problem{Error: err.Error()}, statusFor(err))
			return
		}
		write(w, out, 200)
		return
	}
	if len(parts) == 6 && parts[4] == "conditioning" && parts[5] == "summary" && r.Method == "GET" {
		out, err := s.app.ConditioningSummary(id)
		if err != nil {
			write(w, Problem{Error: err.Error()}, statusFor(err))
			return
		}
		write(w, out, 200)
		return
	}
	if len(parts) == 6 && parts[4] == "measurements" && parts[5] == "report" && r.Method == "GET" {
		revision := 0
		var err error
		if value := r.URL.Query().Get("measurement_revision"); value != "" {
			revision, err = strconv.Atoi(value)
			if err != nil || revision < 1 {
				write(w, Problem{Error: "measurement_revision 无效"}, 400)
				return
			}
		}
		out, appErr := s.app.MeasurementReport(id, revision)
		if appErr != nil {
			code := statusFor(appErr)
			if out.Error != "" {
				code = 409
				write(w, out, code)
			} else {
				write(w, Problem{Error: appErr.Error()}, code)
			}
			return
		}
		write(w, out, 200)
		return
	}
	if r.Method != "POST" {
		write(w, map[string]string{"error": "method"}, 405)
		return
	}
	switch parts[4] {
	case "plan":
		s.plan(w, r, id)
	case "conditioning":
		s.condition(w, r, id)
	case "measurements":
		s.measure(w, r, id)
	case "discrepancies":
		s.decide(w, r, id)
	case "release":
		s.release(w, r, id)
	case "seal":
		s.seal(w, r, id)
	default:
		write(w, map[string]string{"error": "path"}, 404)
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

type baseReq struct {
	RequestID        string `json:"request_id"`
	ExpectedRevision int    `json:"expected_revision"`
}

func (s *Server) plan(w http.ResponseWriter, r *http.Request, id string) {
	var q struct {
		baseReq
		Groups       []string           `json:"groups"`
		TempMin      float64            `json:"temp_min"`
		TempMax      float64            `json:"temp_max"`
		HumMin       float64            `json:"hum_min"`
		HumMax       float64            `json:"hum_max"`
		MinExposure  int                `json:"min_exposure"`
		Metrics      []string           `json:"metrics"`
		Thresholds   map[string]float64 `json:"thresholds"`
		SubmittedBy  string             `json:"submitted_by"`
		ValidateOnly bool               `json:"validate_only"`
	}
	if decode(r, &q) != nil {
		write(w, map[string]string{"error": "invalid"}, 400)
		return
	}
	plan := domain.Plan{Groups: q.Groups, TempMin: q.TempMin, TempMax: q.TempMax, HumMin: q.HumMin, HumMax: q.HumMax, MinExposure: q.MinExposure, Metrics: q.Metrics, Thresholds: q.Thresholds, SubmittedBy: q.SubmittedBy}
	if q.ValidateOnly {
		report, err := s.app.ValidatePlan(id, q.RequestID, q.ExpectedRevision, plan)
		if err != nil {
			write(w, Problem{Error: err.Error()}, statusFor(err))
			return
		}
		write(w, report, 200)
		return
	}
	c, e := s.app.SubmitPlan(id, q.RequestID, q.ExpectedRevision, plan)
	result(w, c, e)
}
func (s *Server) condition(w http.ResponseWriter, r *http.Request, id string) {
	var q struct {
		baseReq
		Readings       []domain.ConditioningReading `json:"readings"`
		ValidateOnly   bool                         `json:"validate_only"`
		Temperature    float64                      `json:"temperature"`
		Humidity       float64                      `json:"humidity"`
		ExposedMinutes int                          `json:"exposed_minutes"`
		At             string                       `json:"at"`
		Confirm        bool                         `json:"confirm"`
		WindowFrom     string                       `json:"window_from"`
		WindowTo       string                       `json:"window_to"`
	}
	if decode(r, &q) != nil {
		write(w, map[string]string{"error": "invalid"}, 400)
		return
	}
	if q.Readings != nil {
		if len(q.Readings) == 0 || len(q.Readings) > 500 {
			write(w, Problem{Error: "readings 数量必须在 1 至 500 之间"}, 400)
			return
		}
		if q.At != "" || q.ExposedMinutes != 0 || q.Temperature != 0 || q.Humidity != 0 || q.WindowFrom != "" || q.WindowTo != "" {
			write(w, Problem{Error: "readings 批量字段不能与单条读数字段混用"}, 400)
			return
		}
		if q.ValidateOnly {
			out, err := s.app.ValidateConditioningBatch(id, q.RequestID, q.ExpectedRevision, q.Readings, q.Confirm)
			if err != nil {
				writeDomainError(w, err)
				return
			}
			write(w, out, 200)
			return
		}
		c, err := s.app.AddConditioningBatch(id, q.RequestID, q.ExpectedRevision, q.Readings, q.Confirm)
		if err != nil {
			writeDomainError(w, err)
			return
		}
		write(w, c, 200)
		return
	}
	if q.ValidateOnly {
		write(w, Problem{Error: "validate_only 仅适用于 readings 批量请求"}, 400)
		return
	}
	if q.Confirm && q.ExposedMinutes == 0 && q.At == "" {
		from, to, err := parseWindow(q.WindowFrom, q.WindowTo)
		if err != nil {
			write(w, Problem{Error: err.Error()}, 400)
			return
		}
		c, appErr := s.app.ConfirmConditioning(id, q.RequestID, q.ExpectedRevision, from, to)
		result(w, c, appErr)
		return
	}
	if q.WindowFrom != "" || q.WindowTo != "" {
		write(w, Problem{Error: "窗口边界仅用于独立确认请求"}, 400)
		return
	}
	at := time.Time{}
	if q.At != "" {
		if t, err := time.Parse(time.RFC3339, q.At); err == nil {
			at = t
		} else {
			write(w, Problem{Error: "invalid at"}, 400)
			return
		}
	}
	c, e := s.app.AddConditioning(id, q.RequestID, q.ExpectedRevision, domain.ConditioningReading{Temperature: q.Temperature, Humidity: q.Humidity, ExposedMinutes: q.ExposedMinutes, At: at}, q.Confirm)
	result(w, c, e)
}
func (s *Server) measure(w http.ResponseWriter, r *http.Request, id string) {
	var q struct {
		baseReq
		Measurements       []domain.Measurement `json:"measurements"`
		RemediationNote    string               `json:"remediation_note"`
		SupersedesRevision int                  `json:"supersedes_revision"`
	}
	if decode(r, &q) != nil {
		write(w, map[string]string{"error": "invalid"}, 400)
		return
	}
	var c *domain.Case
	var e error
	if q.RemediationNote != "" || q.SupersedesRevision != 0 {
		c, e = s.app.RemediationMeasurements(id, q.RequestID, q.ExpectedRevision, q.Measurements, q.RemediationNote, q.SupersedesRevision)
	} else {
		c, e = s.app.Measurements(id, q.RequestID, q.ExpectedRevision, q.Measurements)
	}
	result(w, c, e)
}
func (s *Server) decide(w http.ResponseWriter, r *http.Request, id string) {
	var q struct {
		baseReq
		Items    []domain.Discrepancy `json:"items"`
		ID       string               `json:"id"`
		Metric   string               `json:"metric"`
		Reason   string               `json:"reason"`
		Decision string               `json:"decision"`
		Reviewer string               `json:"reviewer"`
		Retest   []float64            `json:"retest"`
	}
	if decode(r, &q) != nil {
		write(w, map[string]string{"error": "invalid"}, 400)
		return
	}
	if len(q.Items) > 0 {
		c, e := s.app.DecideBatch(id, q.RequestID, q.ExpectedRevision, q.Items)
		result(w, c, e)
		return
	}
	c, e := s.app.Decide(id, q.RequestID, q.ExpectedRevision, domain.Discrepancy{ID: q.ID, Metric: q.Metric, Reason: q.Reason, Decision: q.Decision, Reviewer: q.Reviewer, Retest: q.Retest})
	result(w, c, e)
}
func (s *Server) release(w http.ResponseWriter, r *http.Request, id string) {
	var q struct {
		baseReq
		Approved     bool   `json:"approved"`
		Decision     string `json:"decision"`
		Reason       string `json:"reason"`
		By           string `json:"by"`
		SnapshotHash string `json:"snapshot_hash"`
	}
	if decode(r, &q) != nil {
		write(w, map[string]string{"error": "invalid"}, 400)
		return
	}
	if q.Decision != "" {
		switch strings.ToUpper(q.Decision) {
		case "APPROVE", "APPROVED", "PASS":
			q.Approved = true
		case "REJECT", "REJECTED", "FAIL":
			q.Approved = false
		default:
			write(w, Problem{Error: "decision 无效"}, 400)
			return
		}
	}
	c, e := s.app.Release(id, q.RequestID, q.ExpectedRevision, q.Approved, q.Reason, q.By, q.SnapshotHash)
	result(w, c, e)
}
func (s *Server) seal(w http.ResponseWriter, r *http.Request, id string) {
	var q struct {
		baseReq
		By string `json:"by"`
	}
	if decode(r, &q) != nil {
		write(w, map[string]string{"error": "invalid"}, 400)
		return
	}
	m, e := s.app.Seal(id, q.RequestID, q.ExpectedRevision, q.By)
	if e != nil {
		write(w, map[string]string{"error": e.Error()}, 409)
		return
	}
	write(w, m, 200)
}
func result(w http.ResponseWriter, c *domain.Case, e error) {
	if e != nil {
		write(w, map[string]string{"error": e.Error()}, 409)
		return
	}
	write(w, c, 200)
}

func writeDomainError(w http.ResponseWriter, err error) {
	if indexed, ok := err.(*domain.ConditioningBatchError); ok {
		write(w, map[string]any{"error": indexed.Error(), "index": indexed.Index, "field": indexed.Field, "code": indexed.Code}, http.StatusConflict)
		return
	}
	write(w, Problem{Error: err.Error()}, statusFor(err))
}

func pagination(r *http.Request, defaultPage, defaultSize int) (int, int, error) {
	page, size := defaultPage, defaultSize
	var err error
	if value := r.URL.Query().Get("page"); value != "" {
		page, err = strconv.Atoi(value)
		if err != nil {
			return 0, 0, err
		}
	}
	if value := r.URL.Query().Get("page_size"); value != "" {
		size, err = strconv.Atoi(value)
		if err != nil {
			return 0, 0, err
		}
	}
	if page < 1 || size < 1 || size > 100 {
		return 0, 0, strconv.ErrRange
	}
	return page, size, nil
}

func parseWindow(fromValue, toValue string) (time.Time, time.Time, error) {
	if fromValue == "" && toValue == "" {
		return time.Time{}, time.Time{}, nil
	}
	from, err := time.Parse(time.RFC3339, fromValue)
	if err != nil {
		return time.Time{}, time.Time{}, &time.ParseError{Layout: time.RFC3339, Value: fromValue, LayoutElem: "window_from", ValueElem: fromValue}
	}
	to, err := time.Parse(time.RFC3339, toValue)
	if err != nil || !to.After(from) {
		return time.Time{}, time.Time{}, &time.ParseError{Layout: time.RFC3339, Value: toValue, LayoutElem: "window_to", ValueElem: toValue}
	}
	return from.UTC(), to.UTC(), nil
}

func allowedEventType(value string) bool {
	allowed := map[string]bool{"CASE_CREATED": true, "PLAN_SUBMITTED": true, "CONDITIONING_READING": true, "CONDITIONING_BATCH_RECORDED": true, "CONDITIONED": true, "MEASUREMENTS_RECORDED": true, "REMEDIATION_MEASUREMENTS_RECORDED": true, "DISCREPANCY_DECIDED": true, "REVIEW_RESULT": true, "RELEASE_DECISION": true, "SEALED": true}
	return allowed[value]
}

func statusFor(err error) int {
	message := err.Error()
	if strings.Contains(message, "不存在") || strings.Contains(message, "尚未封存") {
		return http.StatusNotFound
	}
	if strings.Contains(message, "参数") || strings.Contains(message, "不能为空") || strings.Contains(message, "无效") || strings.Contains(message, "未知") {
		return http.StatusBadRequest
	}
	return http.StatusConflict
}
