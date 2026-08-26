package application

import (
	"context"

	"guji-paper/internal/domain"
)

type createResult struct {
	caseValue *domain.Case
	err       error
}

// CreateContext 在传输层请求取消时返回对应的 context 错误。
func (s *Service) CreateContext(ctx context.Context, c *domain.Case, rid string) (*domain.Case, error) {
	done := make(chan createResult, 1)
	go func() {
		created, err := s.Create(c, rid)
		done <- createResult{caseValue: created, err: err}
	}()
	select {
	case result := <-done:
		return result.caseValue, result.err
	case <-ctx.Done():
		result := <-done
		if result.err != nil {
			return nil, result.err
		}
		return nil, ctx.Err()
	}
}
