package snapshot_read_alias_test

import (
	"guji-paper/internal/application"
	"guji-paper/internal/domain"
	"guji-paper/internal/store"
	"os"
	"path/filepath"
	"testing"
)

func TestSnapshotReadDoesNotAliasPlanWhenSnapshotUnavailable(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	app := application.New(st)
	c, err := domain.NewCase("alias-case", "batch-1", "宣纸", "修补", "owner", "reviewer", "authorizer")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = app.Create(c, "create-alias"); err != nil {
		t.Fatal(err)
	}
	plan := domain.Plan{
		Groups: []string{"A", "B"}, TempMin: 18, TempMax: 24, HumMin: 45, HumMax: 60,
		MinExposure: 60, Metrics: []string{"tensile", "ph", "color_delta_e", "fiber_change"},
		Thresholds: map[string]float64{"tensile": 5, "ph": 7, "color_delta_e": 3, "fiber_change": 3}, SubmittedBy: "planner",
	}
	if _, err = app.SubmitPlan("alias-case", "plan-alias", 1, plan); err != nil {
		t.Fatal(err)
	}

	snapshot := filepath.Join(dir, "cases.json")
	if err = os.Rename(snapshot, snapshot+".offline"); err != nil {
		t.Fatal(err)
	}
	read := st.Get("alias-case")
	if read == nil || read.Plan == nil {
		t.Fatal("expected cached plan")
	}
	read.Plan.Thresholds["tensile"] = 999
	read.Plan.Groups[0] = "tampered-group"
	read.Plan.Metrics[0] = "tampered-metric"
	again := st.Get("alias-case")
	if again == nil || again.Plan == nil {
		t.Fatal("expected cached plan on second read")
	}
	if got := again.Plan.Thresholds["tensile"]; got == 999 {
		t.Fatalf("TestSnapshotReadDoesNotAliasPlanWhenSnapshotUnavailable: cached read exposed caller mutation: tensile=%v", got)
	}
	if again.Plan.Groups[0] == "tampered-group" || again.Plan.Metrics[0] == "tampered-metric" {
		t.Fatal("TestSnapshotReadDoesNotAliasPlanWhenSnapshotUnavailable: cached read exposed caller mutation in plan slices")
	}
}
