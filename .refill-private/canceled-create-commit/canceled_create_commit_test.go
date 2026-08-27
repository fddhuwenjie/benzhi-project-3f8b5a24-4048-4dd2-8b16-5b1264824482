package canceled_create_commit_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"guji-paper/internal/application"
	"guji-paper/internal/httpapi"
	"guji-paper/internal/store"
)

type observedCancelContext struct {
	context.Context
	done     chan struct{}
	observed chan struct{}
	once     sync.Once
}

func (c *observedCancelContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.observed) })
	return c.done
}

func (c *observedCancelContext) Err() error {
	select {
	case <-c.done:
		return context.Canceled
	default:
		return nil
	}
}

func holdStoreAtEventOpen(t *testing.T, st *store.Store, dir string) func() {
	t.Helper()
	eventPath := filepath.Join(dir, "events.jsonl")
	if err := os.Remove(eventPath); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(eventPath, 0600); err != nil {
		t.Fatal(err)
	}
	holderDone := make(chan struct{})
	go func() {
		_ = st.Events("lock-probe")
		close(holderDone)
	}()
	writer, err := os.OpenFile(eventPath, os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(eventPath); err != nil {
		writer.Close()
		t.Fatal(err)
	}
	if err := os.WriteFile(eventPath, nil, 0644); err != nil {
		writer.Close()
		t.Fatal(err)
	}
	return func() {
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		<-holderDone
	}
}

func TestCanceledCreateDoesNotCommitAfterClientCancellation(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	handler := httpapi.New(application.New(st)).Handler()
	releaseStore := holdStoreAtEventOpen(t, st, dir)

	ctx := &observedCancelContext{Context: context.Background(), done: make(chan struct{}), observed: make(chan struct{})}
	body := []byte(`{"request_id":"cancel-rid","case_id":"cancel-case","batch":"paper-01","material":"古籍竹纸","purpose":"补洞","owner":"owner-a","reviewer":"reviewer-b","authorizer":"authorizer-c"}`)
	req := httptest.NewRequest(http.MethodPost, httpapi.APIPrefix, bytes.NewReader(body)).WithContext(ctx)
	response := httptest.NewRecorder()
	requestDone := make(chan struct{})
	go func() {
		handler.ServeHTTP(response, req)
		close(requestDone)
	}()

	select {
	case <-ctx.observed:
	case <-time.After(2 * time.Second):
		t.Fatal("CreateContext 未观察请求 context")
	}
	close(ctx.done)
	releaseStore()
	select {
	case <-requestDone:
	case <-time.After(2 * time.Second):
		t.Fatal("取消后的建档请求未返回")
	}
	if response.Code < 400 {
		t.Fatalf("取消请求应返回错误，实际状态码为 %d", response.Code)
	}
	if persisted := st.Get("cancel-case"); persisted != nil {
		t.Fatalf("取消请求已返回错误，但仍提交了 case_id=%s revision=%d", persisted.ID, persisted.Revision)
	}
}
