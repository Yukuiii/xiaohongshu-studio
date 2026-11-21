package accounts

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestReserveRestartWithPublishAndScheduleWork(t *testing.T) {
	s, account := publishTaskStore(t)
	ctx := t.Context()
	input := publishTaskInput(account.ID, "before-update")
	task, err := s.EnqueuePublishTask(ctx, input, 0)
	require.NoError(t, err)
	_, err = s.ReserveRestart(ctx)
	require.ErrorIs(t, err, ErrTaskActive)
	_, err = s.FinishPublishTask(ctx, task.ID, TaskOutcome{Status: TaskFailed})
	require.NoError(t, err)

	due := scheduleTestTime("2026-11-22T10:00:00Z")
	job, err := s.CreateScheduledJob(ctx, scheduleTestInput(account.ID), due.Add(-time.Minute))
	require.NoError(t, err)
	release, err := s.ReserveRestart(ctx)
	require.NoError(t, err)
	blocked, err := s.ClaimDueScheduleRuns(ctx, due)
	require.NoError(t, err)
	require.Empty(t, blocked)
	_, err = s.EnqueuePublishTask(ctx, publishTaskInput(account.ID, "during-update"), 0)
	require.ErrorIs(t, err, ErrRestartPending)
	release()

	runs, err := s.ClaimDueScheduleRuns(ctx, due)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	require.Equal(t, job.ID, runs[0].ScheduleID)
	_, err = s.ReserveRestart(ctx)
	require.ErrorIs(t, err, ErrTaskActive)
}
