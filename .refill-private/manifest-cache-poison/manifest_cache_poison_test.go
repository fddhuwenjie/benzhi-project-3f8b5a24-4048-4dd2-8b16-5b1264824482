package manifest_cache_poison_test

import (
	"guji-paper/internal/application"
	"guji-paper/internal/domain"
	"guji-paper/internal/store"
	"os"
	"path/filepath"
	"testing"
)

func TestFailedSealDoesNotExposeUncommittedManifest(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	c, err := domain.NewCase("manifest-cache", "batch", "古籍修复纸", "修补", "owner", "reviewer", "authorizer")
	if err != nil {
		t.Fatal(err)
	}
	c.Status = domain.Released
	c.AppendEvent("CASE_CREATED", map[string]any{"case_id": c.ID}, "manifest-create")
	if err := st.Create(c, "manifest-create"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "manifests.json"), 0755); err != nil {
		t.Fatal(err)
	}
	app := application.New(st)
	if _, err := app.Seal(c.ID, "manifest-seal", c.Revision, "authorizer"); err == nil {
		t.Fatalf("清单无法落盘时 Seal 不应返回成功")
	}
	if current := app.Get(c.ID); current == nil || current.Status != domain.Released {
		t.Fatalf("失败封存改变了个案终态: %v", current)
	}
	if manifest, ok := app.Manifest(c.ID); ok {
		t.Fatalf("失败封存的清单仍可从污染缓存读取: %s", manifest.ManifestID)
	}
}
