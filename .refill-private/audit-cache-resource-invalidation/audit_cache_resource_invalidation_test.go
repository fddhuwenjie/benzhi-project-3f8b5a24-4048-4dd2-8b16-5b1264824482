package audit_cache_resource_invalidation_test

import (
	"encoding/json"
	"guji-paper/internal/application"
	"guji-paper/internal/domain"
	"guji-paper/internal/httpapi"
	"guji-paper/internal/store"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestAuditCacheRevalidatesTamperedEventLog(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	svc := application.New(st)
	c, err := domain.NewCase("case-audit-cache", "batch-1", "paper", "repair", "owner", "reviewer", "authorizer")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Create(c, "request-create-audit-cache"); err != nil {
		t.Fatal(err)
	}

	handler := httpapi.New(svc).Handler()
	auditURL := "/api/v1/qualification-cases/" + c.ID + "/audit?from_revision=1&page=1&page_size=20"
	firstResponse := httptest.NewRecorder()
	handler.ServeHTTP(firstResponse, httptest.NewRequest(http.MethodGet, auditURL, nil))
	var first application.AuditPage
	if err = json.Unmarshal(firstResponse.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if firstResponse.Code != http.StatusOK || !first.Verified {
		t.Fatalf("initial audit should verify: status=%d report=%+v", firstResponse.Code, first)
	}

	logPath := filepath.Join(dir, "events.jsonl")
	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	var record store.Record
	if err = json.Unmarshal(raw, &record); err != nil {
		t.Fatal(err)
	}
	record.Event.Data = map[string]any{"tampered": true}
	tampered, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	tampered = append(tampered, '\n')
	if err = os.WriteFile(logPath, tampered, 0644); err != nil {
		t.Fatal(err)
	}

	secondResponse := httptest.NewRecorder()
	handler.ServeHTTP(secondResponse, httptest.NewRequest(http.MethodGet, auditURL, nil))
	var second application.AuditPage
	if err = json.Unmarshal(secondResponse.Body.Bytes(), &second); err != nil {
		t.Fatal(err)
	}
	if secondResponse.Code != http.StatusConflict || second.Verified {
		t.Fatalf("tampered event log was accepted from stale audit cache: status=%d report=%+v", secondResponse.Code, second)
	}
}
