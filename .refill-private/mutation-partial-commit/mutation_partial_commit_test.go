package mutation_partial_commit_test

import (
	"guji-paper/internal/application"
	"guji-paper/internal/domain"
	"guji-paper/internal/store"
	"os"
	"path/filepath"
	"testing"
)

func TestMutationErrorDoesNotPublishPartialState(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	app := application.New(st)
	c, err := domain.NewCase("partial-mutation", "batch", "古籍修复纸", "修补", "owner", "reviewer", "authorizer")
	if err != nil {
		t.Fatal(err)
	}
	created, err := app.Create(c, "partial-create")
	if err != nil {
		t.Fatal(err)
	}
	casesPath := filepath.Join(dir, "cases.json")
	snapshot, err := os.ReadFile(casesPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(casesPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(casesPath, 0755); err != nil {
		t.Fatal(err)
	}
	_, err = app.SubmitPlan(created.ID, "partial-plan", created.Revision, validPlan())
	if err == nil {
		t.Fatalf("快照写入失败时变更命令不应返回成功")
	}
	after := app.Get(created.ID)
	if after == nil || after.Status != domain.Draft || after.Revision != created.Revision {
		t.Fatalf("变更返回错误后仍发布了部分状态: status=%v revision=%v", after.Status, after.Revision)
	}
	if err := os.Remove(casesPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(casesPath, snapshot, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := app.SubmitPlan(created.ID, "partial-plan", created.Revision, validPlan()); err != nil {
		t.Fatalf("恢复存储后重试失败: %v", err)
	}
	if _, err := store.Open(dir); err != nil {
		t.Fatalf("失败尝试遗留了破坏事件回放的记录: %v", err)
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
