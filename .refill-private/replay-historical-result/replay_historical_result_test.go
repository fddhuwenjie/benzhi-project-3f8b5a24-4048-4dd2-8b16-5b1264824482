package replay_historical_result_test

import (
	"guji-paper/internal/application"
	"guji-paper/internal/domain"
	"guji-paper/internal/store"
	"os"
	"path/filepath"
	"testing"
)

func TestReplayRestoresHistoricalIdempotentResponse(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	app := application.New(st)
	c, err := domain.NewCase("historical-result", "batch", "古籍修复纸", "修补", "owner", "reviewer", "authorizer")
	if err != nil {
		t.Fatal(err)
	}
	created, err := app.Create(c, "historical-create")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.SubmitPlan(created.ID, "historical-plan", created.Revision, validPlan()); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "request_results.json")); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	retryInput, err := domain.NewCase("historical-result", "batch", "古籍修复纸", "修补", "owner", "reviewer", "authorizer")
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := application.New(reopened).Create(retryInput, "historical-create")
	if err != nil {
		t.Fatalf("事件回放后相同请求无法重试: %v", err)
	}
	if replayed.Status != domain.Draft || replayed.Revision != created.Revision {
		t.Fatalf("历史建档请求被恢复成个案终态: status=%s revision=%d", replayed.Status, replayed.Revision)
	}
}

func validPlan() domain.Plan {
	return domain.Plan{
		Groups: []string{"A", "B"}, TempMin: 18, TempMax: 24, HumMin: 45, HumMax: 60, MinExposure: 60,
		Metrics: []string{"tensile", "ph", "color_delta_e", "fiber_change"},
		Thresholds: map[string]float64{"tensile": 5, "ph": 7, "color_delta_e": 3, "fiber_change": 3},
		SubmittedBy: "planner",
	}
}
