package caselockinitializationrace_test

import (
	"guji-paper/internal/application"
	"guji-paper/internal/domain"
	"guji-paper/internal/store"
	"sync"
	"testing"
)

func TestConcurrentCaseLockInitializationIsRaceFree(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service := application.New(st)
	c, err := domain.NewCase("case-lock-race", "batch-1", "宣纸", "补纸", "owner", "reviewer", "authorizer")
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.Create(c, "create-lock-race")
	if err != nil {
		t.Fatal(err)
	}

	plan := domain.Plan{
		Groups:      []string{"blind-a", "blind-b"},
		TempMin:     18,
		TempMax:     24,
		HumMin:      45,
		HumMax:      55,
		MinExposure: 60,
		Metrics:     []string{"tensile", "ph", "color_delta_e", "fiber_change"},
		Thresholds:  map[string]float64{"tensile": 100, "ph": 7, "color_delta_e": 3, "fiber_change": 2},
		SubmittedBy: "researcher",
	}

	start := make(chan struct{})
	ready := sync.WaitGroup{}
	ready.Add(2)
	results := make(chan error, 2)
	for _, requestID := range []string{"plan-lock-race-a", "plan-lock-race-b"} {
		requestID := requestID
		go func() {
			ready.Done()
			<-start
			_, submitErr := service.SubmitPlan(created.ID, requestID, created.Revision, plan)
			results <- submitErr
		}()
	}
	ready.Wait()
	close(start)

	successes := 0
	for i := 0; i < 2; i++ {
		if <-results == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("并发旧 revision 写入应恰好一个成功，实际成功 %d 个", successes)
	}
}
