package application

import (
	"encoding/json"
	"errors"
	"guji-paper/internal/audit"
	"guji-paper/internal/domain"
	"guji-paper/internal/store"
	"sync"
	"time"
)

func payloadHash(v any) string             { b, _ := json.Marshal(v); return string(b) }
func planPayloadHash(p domain.Plan) string { p.SubmittedAt = time.Time{}; return payloadHash(p) }

func createPayload(c *domain.Case) any {
	return struct {
		ID         string `json:"case_id"`
		Batch      string `json:"batch"`
		Material   string `json:"material"`
		Purpose    string `json:"purpose"`
		Owner      string `json:"owner"`
		Reviewer   string `json:"reviewer"`
		Authorizer string `json:"authorizer"`
	}{c.ID, c.Batch, c.Material, c.Purpose, c.Owner, c.Reviewer, c.Authorizer}
}

type Service struct {
	mu        sync.Mutex
	st        *store.Store
	manifests map[string]audit.Manifest
}

func New(st *store.Store) *Service {
	s := &Service{st: st, manifests: map[string]audit.Manifest{}}
	for _, c := range st.All() {
		if c.Status == domain.Sealed {
			if m, ok := st.Manifest(c.ID); ok {
				s.manifests[c.ID] = m
			}
		}
	}
	return s
}
func (s *Service) Create(c *domain.Case, rid string) (*domain.Case, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if rid == "" {
		return nil, errors.New("request_id 不能为空")
	}
	payload := createPayload(c)
	if v, ok := s.st.Request(rid); ok {
		if h, okh := s.st.RequestHash(rid); okh {
			if h != payloadHash(payload) {
				return nil, errors.New("request_id 重复载荷冲突")
			}
		}
		if old, okc := v.(*domain.Case); okc {
			return domain.CloneCase(old), nil
		}
		return nil, errors.New("request_id 重复载荷冲突")
	}
	c.AppendEvent("CASE_CREATED", payload, rid)
	if err := s.st.Create(c, rid); err != nil {
		return nil, err
	}
	return domain.CloneCase(c), nil
}
func (s *Service) mutate(id, rid string, rev int, payload any, fn func(*domain.Case) error) (*domain.Case, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if rid == "" {
		return nil, errors.New("request_id 不能为空")
	}
	hash := payloadHash(struct {
		CaseID           string `json:"case_id"`
		ExpectedRevision int    `json:"expected_revision"`
		Payload          any    `json:"payload"`
	}{id, rev, payload})
	if v, ok := s.st.Request(rid); ok {
		if oldHash, exists := s.st.RequestHash(rid); exists && oldHash != hash {
			return nil, errors.New("request_id 重复载荷冲突")
		}
		if old, okc := v.(*domain.Case); okc {
			return domain.CloneCase(old), nil
		}
		return nil, errors.New("request_id 重复载荷冲突")
	}
	c := s.st.Get(id)
	if c == nil {
		return nil, errors.New("个案不存在")
	}
	if c.Revision != rev {
		return nil, errors.New("revision 冲突")
	}
	if c.Status == domain.Sealed {
		return nil, errors.New("个案已只读")
	}
	old := c.Revision
	if err := fn(c); err != nil {
		return nil, err
	}
	newEvents := make([]domain.Event, 0)
	for _, e := range c.Events {
		if e.Revision > old {
			newEvents = append(newEvents, e)
		}
	}
	if len(newEvents) == 0 {
		return nil, errors.New("写命令未产生领域事件")
	}
	if err := s.st.PutEvents(c, newEvents, rid, c, hash); err != nil {
		return nil, err
	}
	return domain.CloneCase(c), nil
}
func (s *Service) SubmitPlan(id, rid string, rev int, p domain.Plan) (*domain.Case, error) {
	return s.mutate(id, rid, rev, p, func(c *domain.Case) error {
		report := domain.ValidatePlan(c, p, true)
		if !report.Valid {
			return errors.New(report.Errors[0].Message)
		}
		return c.SubmitPlan(report.NormalizedPlan, rid)
	})
}
func (s *Service) AddConditioning(id, rid string, rev int, r domain.ConditioningReading, confirm bool) (*domain.Case, error) {
	return s.mutate(id, rid, rev, struct {
		Reading domain.ConditioningReading `json:"reading"`
		Confirm bool                       `json:"confirm"`
	}{r, confirm}, func(c *domain.Case) error {
		if err := c.AddConditioning(r, rid); err != nil {
			return err
		}
		if confirm {
			return c.ConfirmConditioning(rid)
		}
		return nil
	})
}
func (s *Service) AddConditioningBatch(id, rid string, rev int, readings []domain.ConditioningReading, confirm bool) (*domain.Case, error) {
	payload := struct {
		Readings []domain.ConditioningReading `json:"readings"`
		Confirm  bool                         `json:"confirm"`
	}{readings, confirm}
	return s.mutate(id, rid, rev, payload, func(c *domain.Case) error {
		_, err := c.AddConditioningBatch(readings, confirm, rid)
		return err
	})
}

func (s *Service) ValidateConditioningBatch(id, rid string, rev int, readings []domain.ConditioningReading, confirm bool) (domain.ConditioningBatchResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if rid == "" {
		return domain.ConditioningBatchResult{}, errors.New("request_id 不能为空")
	}
	hash := payloadHash(map[string]any{"case_id": id, "expected_revision": rev, "readings": readings, "confirm": confirm, "validate_only": true})
	var cached domain.ConditioningBatchResult
	if oldHash, ok, err := s.st.Validation(rid, &cached); err != nil {
		return cached, err
	} else if ok {
		if oldHash != hash {
			return domain.ConditioningBatchResult{}, errors.New("request_id 重复载荷冲突")
		}
		return cached, nil
	}
	c := s.st.Get(id)
	if c == nil {
		return domain.ConditioningBatchResult{}, errors.New("个案不存在")
	}
	if c.Revision != rev {
		return domain.ConditioningBatchResult{}, errors.New("revision 冲突")
	}
	result, _, err := domain.PreviewConditioningBatch(c, readings)
	if err != nil {
		return domain.ConditioningBatchResult{}, err
	}
	if confirm && !result.Summary.Confirmable {
		return domain.ConditioningBatchResult{}, errors.New("最长连续合规窗口未达到方案最小暴露时长")
	}
	if err := s.st.SaveValidation(rid, hash, result); err != nil {
		return domain.ConditioningBatchResult{}, err
	}
	return result, nil
}
func (s *Service) ConfirmConditioning(id, rid string, rev int, from, to time.Time) (*domain.Case, error) {
	return s.mutate(id, rid, rev, map[string]any{"window_from": from, "window_to": to, "confirm": true}, func(c *domain.Case) error {
		return c.ConfirmConditioningWindow(from, to, rid)
	})
}
func (s *Service) Measurements(id, rid string, rev int, m []domain.Measurement) (*domain.Case, error) {
	return s.mutate(id, rid, rev, m, func(c *domain.Case) error { return c.AddMeasurements(m, rid) })
}
func (s *Service) RemediationMeasurements(id, rid string, rev int, m []domain.Measurement, note string, supersedesRevision int) (*domain.Case, error) {
	payload := map[string]any{"measurements": m, "remediation_note": note, "supersedes_revision": supersedesRevision}
	return s.mutate(id, rid, rev, payload, func(c *domain.Case) error {
		return c.ReplaceMeasurements(m, note, supersedesRevision, rid)
	})
}
func (s *Service) Decide(id, rid string, rev int, d domain.Discrepancy) (*domain.Case, error) {
	return s.mutate(id, rid, rev, d, func(c *domain.Case) error { return c.DecideDiscrepancy(d, rid) })
}

func (s *Service) DecideBatch(id, rid string, rev int, ds []domain.Discrepancy) (*domain.Case, error) {
	if len(ds) == 0 {
		return nil, errors.New("复测 items 不能为空")
	}
	return s.mutate(id, rid, rev, ds, func(c *domain.Case) error {
		for _, d := range ds {
			if err := c.DecideDiscrepancy(d, rid); err != nil {
				return err
			}
		}
		return nil
	})
}
func (s *Service) Release(id, rid string, rev int, approved bool, reason, by, snapshotHash string) (*domain.Case, error) {
	payload := map[string]any{"approved": approved, "reason": reason, "by": by, "snapshot_hash": snapshotHash}
	return s.mutate(id, rid, rev, payload, func(c *domain.Case) error {
		preview := domain.BuildReleasePreview(c)
		if snapshotHash == "" || snapshotHash != preview.SnapshotHash {
			return errors.New("签署快照已过期")
		}
		if approved && !preview.CanSign {
			return errors.New("放行前置检查未通过")
		}
		return c.Release(approved, reason, by, rid)
	})
}
func (s *Service) Seal(id, rid string, rev int, by string) (audit.Manifest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if rid == "" {
		return audit.Manifest{}, errors.New("request_id 不能为空")
	}
	hash := payloadHash(struct {
		CaseID           string `json:"case_id"`
		ExpectedRevision int    `json:"expected_revision"`
		By               string `json:"by"`
	}{id, rev, by})
	if v, ok := s.st.Request(rid); ok {
		if oldHash, exists := s.st.RequestHash(rid); exists && oldHash != hash {
			return audit.Manifest{}, errors.New("request_id 重复载荷冲突")
		}
		if m, ok := v.(audit.Manifest); ok {
			return m, nil
		}
		if c, ok := v.(*domain.Case); ok && c.ID == id {
			if m, ok := s.st.Manifest(id); ok {
				return m, nil
			}
		}
		return audit.Manifest{}, errors.New("request_id 重复载荷冲突")
	}
	c := s.st.Get(id)
	if c == nil {
		return audit.Manifest{}, errors.New("个案不存在")
	}
	if c.Revision != rev {
		return audit.Manifest{}, errors.New("revision 冲突")
	}
	if c.Status == domain.Sealed {
		return audit.Manifest{}, errors.New("个案已只读")
	}
	if !audit.VerifyEventRange(s.st.Events(id), 1, 0).Verified {
		return audit.Manifest{}, errors.New("审计链校验失败")
	}
	if err := c.Seal(by, rid); err != nil {
		return audit.Manifest{}, err
	}
	m := audit.Build(c, by)
	s.manifests[id] = m
	if err := s.st.SaveManifest(id, m); err != nil {
		return audit.Manifest{}, err
	}
	newEvents := make([]domain.Event, 0, 1)
	for _, e := range c.Events {
		if e.Revision > rev {
			newEvents = append(newEvents, e)
		}
	}
	if err := s.st.PutEvents(c, newEvents, rid, m, hash); err != nil {
		return audit.Manifest{}, err
	}
	return m, nil
}
func (s *Service) Get(id string) *domain.Case { return s.st.Get(id) }
func (s *Service) Manifest(id string) (audit.Manifest, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.st.Manifest(id)
	if ok {
		s.manifests[id] = m
	}
	if !ok {
		if c := s.st.Get(id); c != nil && c.Status == domain.Sealed {
			m = audit.Build(c, "")
			s.manifests[id] = m
			ok = true
		}
	}
	return m, ok
}

func (s *Service) ValidatePlan(id, rid string, rev int, p domain.Plan) (domain.PlanValidationReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if rid == "" {
		return domain.PlanValidationReport{}, errors.New("request_id 不能为空")
	}
	hash := payloadHash(map[string]any{"case_id": id, "expected_revision": rev, "plan": planPayloadHash(p)})
	var cached domain.PlanValidationReport
	if oldHash, ok, err := s.st.Validation(rid, &cached); err != nil {
		return cached, err
	} else if ok {
		if oldHash != hash {
			return domain.PlanValidationReport{}, errors.New("request_id 重复载荷冲突")
		}
		return cached, nil
	}
	c := s.st.Get(id)
	if c == nil {
		return domain.PlanValidationReport{}, errors.New("个案不存在")
	}
	if c.Revision != rev {
		return domain.PlanValidationReport{}, errors.New("revision 冲突")
	}
	report := domain.ValidatePlan(c, p, true)
	if err := s.st.SaveValidation(rid, hash, report); err != nil {
		return domain.PlanValidationReport{}, err
	}
	return report, nil
}
