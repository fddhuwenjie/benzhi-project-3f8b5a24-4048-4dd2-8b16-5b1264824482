package create_snapshot_loss_test

import (
	"guji-paper/internal/application"
	"guji-paper/internal/domain"
	"guji-paper/internal/store"
	"os"
	"path/filepath"
	"testing"
)

func TestCreateNeverAcknowledgesLostSnapshot(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "cases.json"), 0755); err != nil {
		t.Fatal(err)
	}
	c, err := domain.NewCase("lost-create", "batch", "古籍修复纸", "修补", "owner", "reviewer", "authorizer")
	if err != nil {
		t.Fatal(err)
	}
	app := application.New(st)
	_, createErr := app.Create(c, "lost-create-request")
	if createErr == nil {
		reopened, err := store.Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		if reopened.Get("lost-create") == nil {
			t.Fatalf("Create 返回成功，但重启后已确认的个案消失")
		}
		return
	}
	if app.Get("lost-create") != nil {
		t.Fatalf("Create 返回错误后仍发布了内存状态")
	}
	if err := os.Remove(filepath.Join(dir, "cases.json")); err != nil {
		t.Fatal(err)
	}
	retry, err := domain.NewCase("lost-create", "batch", "古籍修复纸", "修补", "owner", "reviewer", "authorizer")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.Create(retry, "lost-create-request"); err != nil {
		t.Fatalf("恢复存储后重试失败: %v", err)
	}
	if _, err := store.Open(dir); err != nil {
		t.Fatalf("失败尝试遗留了破坏事件回放的记录: %v", err)
	}
}
