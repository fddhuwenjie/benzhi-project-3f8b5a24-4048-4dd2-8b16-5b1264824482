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
// dirPath returns the store's data directory.
func (s *Store) dirPath() string { return filepath.Dir(s.path) }

// appendEventsLocked appends the given records to the event log and returns
// the file offset before the append (for rollback). The caller holds s.mu.
func (s *Store) appendEventsLocked(records []byte) (int64, error) {
	info, err := os.Stat(s.path)
	if err != nil {
		return 0, err
	}
	offset := info.Size()
	f, err := os.OpenFile(s.path, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	if _, err := f.Write(records); err != nil {
		return 0, err
	}
	if err := f.Sync(); err != nil {
		return 0, err
	}
	return offset, nil
}

// truncateEventsLocked truncates the event log to the given offset, undoing a
// partial append. Errors are best-effort because this runs on a rollback path.
func (s *Store) truncateEventsLocked(offset int64) {
	_ = os.Truncate(s.path, offset)
	f, err := os.OpenFile(s.path, os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	_ = f.Sync()
	f.Close()
}

// writeSnapshotLocked writes a JSON snapshot file.
func (s *Store) writeSnapshotLocked(name string, data []byte) error {
	return os.WriteFile(filepath.Join(s.dirPath(), name), data, 0644)
}

// snapshotBytes reads the current on-disk bytes of a snapshot file so a
// rollback can restore the exact pre-command content.
func (s *Store) snapshotBytes(name string) []byte {
	b, _ := os.ReadFile(filepath.Join(s.dirPath(), name))
	return b
}

// restoreSnapshotLocked writes the captured bytes back to the snapshot file.
func (s *Store) restoreSnapshotLocked(name string, data []byte) {
	if data == nil {
		_ = os.Remove(filepath.Join(s.dirPath(), name))
		return
	}
	_ = os.WriteFile(filepath.Join(s.dirPath(), name), data, 0644)
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

	// Capture the pre-command on-disk snapshot bytes so any failure can
	// restore them exactly, and the pre-command in-memory state.
	prevCasesBytes := s.snapshotBytes("cases.json")
	prevRequestsBytes := s.snapshotBytes("requests.json")
	prevRRBytes := s.snapshotBytes("request_results.json")
	prevCase, prevCaseExists := s.cases[c.ID]
	prevEvents := append([]domain.Event(nil), s.events[c.ID]...)
	prevResult, prevResultExists := s.requests[rid]
	prevHash, prevHashExists := s.requestHashes[rid]
	prevRR, prevRRExists := s.requestResults[rid]

	// Build the event-log records and append them (intent to commit).
	var records bytes.Buffer
	for _, event := range events {
		b, _ := json.Marshal(Record{CaseID: c.ID, Event: event})
		records.Write(b)
		records.WriteByte('\n')
	}
	offset, err := s.appendEventsLocked(records.Bytes())
	if err != nil {
		return err
	}

	// Mutate in-memory state to the post-command view.
	s.cases[c.ID] = domain.CloneCase(c)
	s.events[c.ID] = append(s.events[c.ID], events...)
	s.requests[rid] = result
	if requestHash == "" && len(events) > 0 {
		b, _ := json.Marshal(events[0].Data)
		requestHash = string(b)
	}
	s.requestHashes[rid] = requestHash

	// Persist the derived snapshots. If any step fails, roll back the event
	// log, the on-disk snapshots, and all in-memory state so the caller,
	// readers, and restarts all observe the pre-command state.
	restoreMemory := func() {
		if prevCaseExists {
			s.cases[c.ID] = prevCase
		} else {
			delete(s.cases, c.ID)
		}
		s.events[c.ID] = prevEvents
		if len(s.events[c.ID]) == 0 {
			delete(s.events, c.ID)
		}
		if prevResultExists {
			s.requests[rid] = prevResult
		} else {
			delete(s.requests, rid)
		}
		if prevHashExists {
			s.requestHashes[rid] = prevHash
		} else {
			delete(s.requestHashes, rid)
		}
		if prevRRExists {
			s.requestResults[rid] = prevRR
		} else {
			delete(s.requestResults, rid)
		}
	}
	rollback := func() {
		s.restoreSnapshotLocked("cases.json", prevCasesBytes)
		s.restoreSnapshotLocked("requests.json", prevRequestsBytes)
		s.restoreSnapshotLocked("request_results.json", prevRRBytes)
		restoreMemory()
		s.truncateEventsLocked(offset)
	}

	if sb, e := json.Marshal(s.cases); e == nil {
		if err = s.writeSnapshotLocked("cases.json", sb); err != nil {
			rollback()
			return err
		}
	}
	if hb, e := json.Marshal(s.requestHashes); e == nil {
		if err = s.writeSnapshotLocked("requests.json", hb); err != nil {
			rollback()
			return err
		}
	}
	if err = s.saveRequestResultLocked(rid, result); err != nil {
		rollback()
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

	// Capture pre-command on-disk snapshot bytes and in-memory state.
	prevCasesBytes := s.snapshotBytes("cases.json")
	prevRequestsBytes := s.snapshotBytes("requests.json")
	prevRRBytes := s.snapshotBytes("request_results.json")
	prevResult, prevResultExists := s.requests[rid]
	prevHash, prevHashExists := s.requestHashes[rid]
	prevRR, prevRRExists := s.requestResults[rid]

	ev := c.Events[len(c.Events)-1]
	rec, _ := json.Marshal(Record{CaseID: c.ID, Event: ev})
	offset, err := s.appendEventsLocked(append(rec, '\n'))
	if err != nil {
		return err
	}

	// Mutate in-memory state to the post-command view.
	s.cases[c.ID] = domain.CloneCase(c)
	s.events[c.ID] = append(s.events[c.ID], ev)
	s.requests[rid] = c
	requestHash := ""
	if b, e := json.Marshal(ev.Data); e == nil {
		requestHash = string(b)
	}
	s.requestHashes[rid] = requestHash

	restoreMemory := func() {
		delete(s.cases, c.ID)
		delete(s.events, c.ID)
		if prevResultExists {
			s.requests[rid] = prevResult
		} else {
			delete(s.requests, rid)
		}
		if prevHashExists {
			s.requestHashes[rid] = prevHash
		} else {
			delete(s.requestHashes, rid)
		}
		if prevRRExists {
			s.requestResults[rid] = prevRR
		} else {
			delete(s.requestResults, rid)
		}
	}
	rollback := func() {
		s.restoreSnapshotLocked("cases.json", prevCasesBytes)
		s.restoreSnapshotLocked("requests.json", prevRequestsBytes)
		s.restoreSnapshotLocked("request_results.json", prevRRBytes)
		restoreMemory()
		s.truncateEventsLocked(offset)
	}

	if sb, e := json.Marshal(s.cases); e == nil {
		if err = s.writeSnapshotLocked("cases.json", sb); err != nil {
			rollback()
			return err
		}
	}
	if hb, e := json.Marshal(s.requestHashes); e == nil {
		if err = s.writeSnapshotLocked("requests.json", hb); err != nil {
			rollback()
			return err
		}
	}
	if err = s.saveRequestResultLocked(rid, c); err != nil {
		rollback()
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
	s.validations[rid] = CachedValidation{PayloadHash: payloadHash, Result: b}
	all, err := json.Marshal(s.validations)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(filepath.Dir(s.path), "validations.json"), all, 0644)
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
