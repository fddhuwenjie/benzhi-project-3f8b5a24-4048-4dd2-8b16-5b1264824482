package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"guji-paper/internal/application"
	"guji-paper/internal/httpapi"
	"guji-paper/internal/store"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:19081", "监听地址")
	self := flag.Bool("self-check", false, "自检")
	data := flag.String("data", "./data", "数据目录")
	flag.Parse()
	if p := os.Getenv("PORT"); p != "" && *addr == "127.0.0.1:19081" {
		*addr = "127.0.0.1:" + p
	}
	if !validAddr(*addr) {
		os.Exit(2)
	}
	dir := *data
	if *self {
		d, _ := os.MkdirTemp("", "paper-self")
		defer os.RemoveAll(d)
		dir = d
	}
	st, e := store.Open(dir)
	if e != nil {
		panic(e)
	}
	a := application.New(st)
	s := httpapi.New(a)
	if *self {
		runSelf(*addr, s)
		return
	}
	h := &http.Server{Addr: *addr, Handler: s.Handler()}
	fmt.Println("监听", *addr)
	if e := h.ListenAndServe(); e != nil && e != http.ErrServerClosed {
		panic(e)
	}
}
func validAddr(a string) bool {
	h, p, e := net.SplitHostPort(a)
	if e != nil || h != "127.0.0.1" {
		return false
	}
	n, e := strconv.Atoi(p)
	return e == nil && n > 1024 && n < 65536
}
func runSelf(addr string, s *httpapi.Server) {
	h := &http.Server{Addr: addr, Handler: s.Handler()}
	go h.ListenAndServe()
	time.Sleep(100 * time.Millisecond)
	base := "http://" + addr
	post := func(path string, v any) map[string]any {
		b, _ := json.Marshal(v)
		resp, e := http.Post(base+path, "application/json", strings.NewReader(string(b)))
		if e != nil {
			panic(e)
		}
		defer resp.Body.Close()
		var m map[string]any
		json.NewDecoder(resp.Body).Decode(&m)
		if resp.StatusCode >= 400 {
			panic(fmt.Sprintf("self-check %s failed: %v", path, m))
		}
		return m
	}
	get := func(path string) map[string]any {
		resp, e := http.Get(base + path)
		if e != nil {
			panic(e)
		}
		defer resp.Body.Close()
		var m map[string]any
		json.NewDecoder(resp.Body).Decode(&m)
		if resp.StatusCode >= 400 {
			panic(fmt.Sprintf("self-check %s failed: %v", path, m))
		}
		return m
	}
	c := post("/api/v1/qualification-cases", map[string]any{"request_id": "r1", "case_id": "c1", "batch": "B1", "material": "宣纸", "purpose": "修补", "owner": "u1", "reviewer": "u2", "authorizer": "u3"})
	rev := getRev(c)
	c = post("/api/v1/qualification-cases/c1/plan", map[string]any{"request_id": "r2", "expected_revision": rev, "groups": []string{"A", "B"}, "temp_min": 18, "temp_max": 24, "hum_min": 45, "hum_max": 60, "min_exposure": 60, "metrics": []string{"tensile", "ph", "color_delta_e", "fiber_change"}, "thresholds": map[string]float64{"tensile": 5, "ph": 7, "color_delta_e": 2, "fiber_change": 2}, "submitted_by": "u4"})
	rev = getRev(c)
	for i := 0; i < 2; i++ {
		c = post("/api/v1/qualification-cases/c1/conditioning", map[string]any{"request_id": fmt.Sprintf("r3%d", i), "expected_revision": rev, "temperature": 21, "humidity": 50, "exposed_minutes": 30, "confirm": i == 1})
		rev = getRev(c)
	}
	c = post("/api/v1/qualification-cases/c1/measurements", map[string]any{"request_id": "r5", "expected_revision": rev, "measurements": []any{map[string]any{"group": "A", "measured_by": "u1", "tensile": 10, "ph_value": 6, "color_delta_e": 1, "fiber_dimension_change_rate": 1}, map[string]any{"group": "B", "measured_by": "u1", "tensile": 10.05, "ph_value": 6, "color_delta_e": 1, "fiber_dimension_change_rate": 1}}})
	rev = getRev(c)
	preview := get("/api/v1/qualification-cases/c1/release")
	c = post("/api/v1/qualification-cases/c1/release", map[string]any{"request_id": "r6", "expected_revision": rev, "approved": true, "reason": "自检阈值符合", "by": "u3", "snapshot_hash": preview["snapshot_hash"]})
	rev = getRev(c)
	post("/api/v1/qualification-cases/c1/seal", map[string]any{"request_id": "r7", "expected_revision": rev, "by": "u3"})
	manifest := get("/api/v1/qualification-cases/c1/manifest?include_entries=true")
	if manifest["verified"] != true {
		panic("self-check manifest verification failed")
	}
	fmt.Println("self-check ok")
	h.Close()
}
func getRev(m map[string]any) int {
	if v, ok := m["Revision"].(float64); ok {
		return int(v)
	}
	if v, ok := m["revision"].(float64); ok {
		return int(v)
	}
	return 0
}
