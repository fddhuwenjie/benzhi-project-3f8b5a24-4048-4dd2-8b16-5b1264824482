package store

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"guji-paper/internal/audit"
	"guji-paper/internal/domain"
	"os"
	"path/filepath"
	"sync"
)

type Record struct {
	CaseID string       `json:"case_id"`
	Event  domain.Event `json:"event"`
}
type Store struct {
	mu             sync.Mutex
	path           string
	cases          map[string]*domain.Case
	requests       map[string]any
	requestHashes  map[string]string
	manifests      map[string]audit.Manifest
	events         map[string][]domain.Event
	validations    map[string]CachedValidation
	requestResults map[string]CachedRequestResult
}

type CachedValidation struct {
	PayloadHash string          `json:"payload_hash"`
	Result      json.RawMessage `json:"result"`
}

type CachedRequestResult struct {
	Kind   string          `json:"kind"`
	Result json.RawMessage `json:"result"`
}

func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	s := &Store{path: filepath.Join(dir, "events.jsonl"), cases: map[string]*domain.Case{}, requests: map[string]any{}, requestHashes: map[string]string{}, manifests: map[string]audit.Manifest{}, events: map[string][]domain.Event{}, validations: map[string]CachedValidation{}, requestResults: map[string]CachedRequestResult{}}
	if b, e := os.ReadFile(filepath.Join(dir, "requests.json")); e == nil {
		_ = json.Unmarshal(b, &s.requestHashes)
	}
	if b, e := os.ReadFile(filepath.Join(dir, "manifests.json")); e == nil {
		_ = json.Unmarshal(b, &s.manifests)
	}
	if b, e := os.ReadFile(filepath.Join(dir, "validations.json")); e == nil {
		_ = json.Unmarshal(b, &s.validations)
	}
	if b, e := os.ReadFile(filepath.Join(dir, "request_results.json")); e == nil {
		_ = json.Unmarshal(b, &s.requestResults)
		for rid, cached := range s.requestResults {
			switch cached.Kind {
			case "case":
				var result domain.Case
				if json.Unmarshal(cached.Result, &result) == nil {
					s.requests[rid] = domain.CloneCase(&result)
				}
			case "manifest":
				var result audit.Manifest
				if json.Unmarshal(cached.Result, &result) == nil {
					s.requests[rid] = result
				}
			}
		}
	}
	if b, e := os.ReadFile(filepath.Join(dir, "cases.json")); e == nil {
		json.Unmarshal(b, &s.cases)
	}
	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_RDONLY, 0644)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	last := map[string]string{}
	revs := map[string]int{}
	for sc.Scan() {
		var rec Record
		if json.Unmarshal(sc.Bytes(), &rec) != nil {
			continue
		}
		if _, ok := s.cases[rec.CaseID]; !ok {
			continue
		}
		if rec.Event.Revision != revs[rec.CaseID]+1 || rec.Event.PrevHash != last[rec.CaseID] {
			return nil, errors.New("审计链校验失败")
		}
		if rec.Event.Hash != domain.ComputeEventHash(rec.Event) {
			return nil, errors.New("审计链校验失败")
		}
		revs[rec.CaseID] = rec.Event.Revision
		last[rec.CaseID] = rec.Event.Hash
		s.events[rec.CaseID] = append(s.events[rec.CaseID], rec.Event)
		if rec.Event.RequestID != "" {
			if _, restored := s.requests[rec.Event.RequestID]; !restored {
				s.requests[rec.Event.RequestID] = s.cases[rec.CaseID]
			}
			if _, ok := s.requestHashes[rec.Event.RequestID]; !ok {
				if b, e := json.Marshal(rec.Event.Data); e == nil {
					s.requestHashes[rec.Event.RequestID] = string(b)
				}
			}
		}
	}
	return s, nil
}

func (s *Store) RequestHash(rid string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	h, ok := s.requestHashes[rid]
	return h, ok
}
func (s *Store) SaveManifest(id string, m audit.Manifest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.manifests[id] = m
	b, e := json.Marshal(s.manifests)
	if e != nil {
		return e
	}
	return os.WriteFile(filepath.Join(filepath.Dir(s.path), "manifests.json"), b, 0644)
}
func (s *Store) Manifest(id string) (audit.Manifest, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// 每次读取持久化副本，使只读核验能够发现运行期间的磁盘篡改。
	if b, err := os.ReadFile(filepath.Join(filepath.Dir(s.path), "manifests.json")); err == nil {
		var manifests map[string]audit.Manifest
		if json.Unmarshal(b, &manifests) == nil {
			s.manifests = manifests
		}
	}
	v, ok := s.manifests[id]
	return v, ok
}
func (s *Store) Get(id string) *domain.Case {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshCasesLocked()
	return domain.CloneCase(s.cases[id])
}
func (s *Store) PutCase(c *domain.Case, ev domain.Event, rid string, result any) error {
	return s.PutEvents(c, []domain.Event{ev}, rid, result, "")
}

func (s *Store) PutEvents(c *domain.Case, events []domain.Event, rid string, result any, requestHash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if rid == "" {
		return errors.New("request_id 不能为空")
	}
	if oldHash, ok := s.requestHashes[rid]; ok && requestHash != "" && oldHash != requestHash {
		return errors.New("request_id 重复载荷冲突")
	}
	f, err := os.OpenFile(s.path, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	var records bytes.Buffer
	for _, event := range events {
		b, _ := json.Marshal(Record{CaseID: c.ID, Event: event})
		records.Write(b)
		records.WriteByte('\n')
	}
	if _, err = f.Write(records.Bytes()); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	s.cases[c.ID] = domain.CloneCase(c)
	s.events[c.ID] = append(s.events[c.ID], events...)
	sb, _ := json.Marshal(s.cases)
	if err = os.WriteFile(filepath.Join(filepath.Dir(s.path), "cases.json"), sb, 0644); err != nil {
		return err
	}
	s.requests[rid] = result
	if requestHash == "" && len(events) > 0 {
		b, _ := json.Marshal(events[0].Data)
		requestHash = string(b)
	}
	s.requestHashes[rid] = requestHash
	if hb, e := json.Marshal(s.requestHashes); e == nil {
		if err = os.WriteFile(filepath.Join(filepath.Dir(s.path), "requests.json"), hb, 0644); err != nil {
			return err
		}
	}
	if err = s.saveRequestResultLocked(rid, result); err != nil {
		return err
	}
	return nil
}
func (s *Store) Create(c *domain.Case, rid string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.cases[c.ID]; ok {
		return errors.New("case_id 已存在")
	}
	f, err := os.OpenFile(s.path, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	ev := c.Events[len(c.Events)-1]
	b, _ := json.Marshal(Record{CaseID: c.ID, Event: ev})
	f.Write(append(b, '\n'))
	f.Sync()
	s.cases[c.ID] = domain.CloneCase(c)
	s.events[c.ID] = append(s.events[c.ID], ev)
	sb, _ := json.Marshal(s.cases)
	os.WriteFile(filepath.Join(filepath.Dir(s.path), "cases.json"), sb, 0644)
	s.requests[rid] = c
	if b, e := json.Marshal(ev.Data); e == nil {
		s.requestHashes[rid] = string(b)
		if hb, e2 := json.Marshal(s.requestHashes); e2 == nil {
			_ = os.WriteFile(filepath.Join(filepath.Dir(s.path), "requests.json"), hb, 0644)
		}
	}
	if err := s.saveRequestResultLocked(rid, c); err != nil {
		return err
	}
	return nil
}

func (s *Store) saveRequestResultLocked(rid string, result any) error {
	kind := ""
	switch result.(type) {
	case *domain.Case, domain.Case:
		kind = "case"
	case audit.Manifest, *audit.Manifest:
		kind = "manifest"
	default:
		return errors.New("不支持的幂等结果类型")
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	s.requestResults[rid] = CachedRequestResult{Kind: kind, Result: raw}
	all, err := json.Marshal(s.requestResults)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(filepath.Dir(s.path), "request_results.json"), all, 0644)
}
func (s *Store) Request(rid string) (any, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.requests[rid]
	return v, ok
}
func (s *Store) All() []*domain.Case {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshCasesLocked()
	out := make([]*domain.Case, 0, len(s.cases))
	for _, c := range s.cases {
		out = append(out, domain.CloneCase(c))
	}
	return out
}

func (s *Store) Events(id string) []domain.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	events := s.events[id]
	if file, err := os.Open(s.path); err == nil {
		defer file.Close()
		fromDisk := make([]domain.Event, 0, len(events))
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			var record Record
			if json.Unmarshal(scanner.Bytes(), &record) == nil && record.CaseID == id {
				fromDisk = append(fromDisk, record.Event)
			}
		}
		if len(fromDisk) > 0 {
			events = fromDisk
		}
	}
	out := make([]domain.Event, len(events))
	copy(out, events)
	return out
}

func (s *Store) refreshCasesLocked() {
	b, err := os.ReadFile(filepath.Join(filepath.Dir(s.path), "cases.json"))
	if err != nil {
		return
	}
	var cases map[string]*domain.Case
	if json.Unmarshal(b, &cases) == nil {
		for id, c := range cases {
			cases[id] = domain.CloneCase(c)
		}
		s.cases = cases
	}
}

func (s *Store) SaveValidation(rid, payloadHash string, result any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if rid == "" {
		return errors.New("request_id 不能为空")
	}
	if old, ok := s.validations[rid]; ok {
		if old.PayloadHash != payloadHash {
			return errors.New("request_id 重复载荷冲突")
		}
		return nil
	}
	b, err := json.Marshal(result)
	if err != nil {
		return err
	}
	updated := make(map[string]CachedValidation, len(s.validations)+1)
	for k, v := range s.validations {
		updated[k] = v
	}
	updated[rid] = CachedValidation{PayloadHash: payloadHash, Result: b}
	all, err := json.Marshal(updated)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(s.path), "validations.json"), all, 0644); err != nil {
		return err
	}
	s.validations = updated
	return nil
}

func (s *Store) Validation(rid string, result any) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.validations[rid]
	if !ok {
		return "", false, nil
	}
	if err := json.Unmarshal(value.Result, result); err != nil {
		return "", false, err
	}
	return value.PayloadHash, true, nil
}
