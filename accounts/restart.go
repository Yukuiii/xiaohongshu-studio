package accounts

import (
	"context"
	"os"
)

// ReserveRestart 确认没有活动发布或定时执行，并在退出前暂停新任务入队。
func (s *Store) ReserveRestart(ctx context.Context) (func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, os.ErrClosed
	}
	if s.restartPending {
		return nil, ErrRestartPending
	}
	var busy bool
	if err := s.db.QueryRowContext(ctx, `SELECT
		EXISTS(SELECT 1 FROM publish_tasks WHERE status IN ('queued', 'running')) OR
		EXISTS(SELECT 1 FROM schedule_runs WHERE status IN ('running', 'publishing'))`).Scan(&busy); err != nil {
		return nil, err
	}
	if busy {
		return nil, ErrTaskActive
	}
	s.restartPending = true
	return func() {
		s.mu.Lock()
		s.restartPending = false
		s.mu.Unlock()
	}, nil
}
