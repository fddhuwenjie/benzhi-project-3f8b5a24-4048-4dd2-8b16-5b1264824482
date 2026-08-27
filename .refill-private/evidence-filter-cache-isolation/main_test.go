package evidence_filter_cache_isolation_test

import (
	"guji-paper/internal/application"
	"guji-paper/internal/audit"
	"guji-paper/internal/domain"
	"guji-paper/internal/store"
	"testing"
)

func TestEvidenceSelectionCacheIsolatedByFilter(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	c := &domain.Case{
		ID: "cache-filter-case", Status: domain.Draft,
		Measurements: map[string]domain.Measurement{}, Discrepancies: map[string]domain.Discrepancy{},
	}
	c.AppendEvent("CASE_CREATED", map[string]any{"case_id": c.ID}, "create-cache-filter")
	if err := st.Create(c, "create-cache-filter"); err != nil {
		t.Fatal(err)
	}

	c.Status = domain.PlanReady
	c.AppendEvent("PLAN_SUBMITTED", map[string]any{"plan_id": "plan-cache-filter"}, "plan-cache-filter")
	planEvent := c.Events[len(c.Events)-1]
	if err := st.PutEvents(c, []domain.Event{planEvent}, "plan-cache-filter", c, "plan-cache-filter-payload"); err != nil {
		t.Fatal(err)
	}

	c.Status = domain.Sealed
	c.AppendEvent("SEALED", map[string]any{"sealed_by": "authorizer"}, "seal-cache-filter")
	manifest := audit.Build(c, "authorizer")
	if err := st.SaveManifest(c.ID, manifest); err != nil {
		t.Fatal(err)
	}
	sealEvent := c.Events[len(c.Events)-1]
	if err := st.PutEvents(c, []domain.Event{sealEvent}, "seal-cache-filter", manifest, "seal-cache-filter-payload"); err != nil {
		t.Fatal(err)
	}

	service := application.New(st)
	filing, err := service.EvidenceCredential(c.ID, evidenceQuery("filing", c.Revision))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := service.EvidenceCredential(c.ID, evidenceQuery("plan", c.Revision))
	if err != nil {
		t.Fatal(err)
	}

	if len(plan.Items) != 1 || plan.Items[0].EventType != "PLAN_SUBMITTED" || plan.EvidenceStage != "plan" {
		t.Fatalf("plan 查询复用了 filing 缓存: filing=%+v plan=%+v", filing, plan)
	}
	if plan.SelectionSHA256 == filing.SelectionSHA256 {
		t.Fatalf("不同筛选条件返回相同 selection_sha256: %s", plan.SelectionSHA256)
	}
}

func evidenceQuery(stage string, toRevision int) application.EvidenceQuery {
	return application.EvidenceQuery{
		Stage: stage, FromRevision: 1, ToRevision: toRevision, Page: 1, PageSize: 20,
	}
}
