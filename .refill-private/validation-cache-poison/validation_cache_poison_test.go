package validation_cache_poison_test

import (
	"guji-paper/internal/application"
	"guji-paper/internal/domain"
	"guji-paper/internal/store"
	"os"
	"path/filepath"
	"testing"
)

func TestFailedValidationWriteDoesNotPopulateRetryCache(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	app := application.New(st)
	c, err := domain.NewCase("validation-cache", "batch", "古籍修复纸", "修补", "owner", "reviewer", "authorizer")
	if err != nil {
		t.Fatal(err)
	}
	created, err := app.Create(c, "validation-create")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "validations.json"), 0755); err != nil {
		t.Fatal(err)
	}
	plan := domain.Plan{
		Groups: []string{"A", "B"}, TempMin: 18, TempMax: 24, HumMin: 45, HumMax: 60, MinExposure: 60,
		Metrics: []string{"tensile", "ph", "color_delta_e", "fiber_change"},
		Thresholds: map[string]float64{"tensile": 5, "ph": 7, "color_delta_e": 3, "fiber_change": 3},
		SubmittedBy: "planner",
	}
	if _, err := app.ValidatePlan(created.ID, "validation-retry", created.Revision, plan); err == nil {
		t.Fatalf("首次预校验应报告缓存落盘失败")
	}
	if _, err := app.ValidatePlan(created.ID, "validation-retry", created.Revision, plan); err == nil {
		t.Fatalf("失败写入污染了内存缓存，重试被错误当作成功")
	}
}
