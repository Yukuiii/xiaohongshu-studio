package accounts

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDraftRevisionsAndDelete(t *testing.T) {
	s := openStore(t, filepath.Join(t.TempDir(), "accounts"))
	a := s.List()[0]

	draft, found, err := s.LoadDraft(a.ID)
	require.NoError(t, err)
	require.False(t, found)
	require.Zero(t, draft.Revision)

	first, err := s.SaveDraft(a.ID, []byte(`{"title":"第一版"}`), 0)
	require.NoError(t, err)
	require.Equal(t, int64(1), first.Revision)
	_, err = s.SaveDraft(a.ID, []byte(`{"title":"冲突"}`), 0)
	require.ErrorIs(t, err, ErrRevisionConflict)
	second, err := s.SaveDraft(a.ID, []byte(`{"title":"第二版"}`), 1)
	require.NoError(t, err)
	require.Equal(t, int64(2), second.Revision)

	loaded, found, err := s.LoadDraft(a.ID)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, int64(2), loaded.Revision)
	require.JSONEq(t, `{"title":"第二版"}`, string(loaded.Payload))

	require.NoError(t, s.DeleteDraft(a.ID))
	_, found, err = s.LoadDraft(a.ID)
	require.NoError(t, err)
	require.False(t, found)
	revision, err := s.DraftRevision(a.ID)
	require.NoError(t, err)
	require.Zero(t, revision)
}

func TestPublishProtectionRevisions(t *testing.T) {
	s := openStore(t, filepath.Join(t.TempDir(), "accounts"))
	a := s.List()[0]

	empty, err := s.LoadProtection(a.ID)
	require.NoError(t, err)
	require.Zero(t, empty.Revision)
	require.False(t, empty.Uncertain)

	first, err := s.SaveProtection(a.ID, 0, true, "", nil)
	require.NoError(t, err)
	require.Equal(t, int64(1), first.Revision)
	require.True(t, first.Uncertain)
	_, err = s.SaveProtection(a.ID, 0, true, "", nil)
	require.ErrorIs(t, err, ErrRevisionConflict)

	reviewed := int64(1)
	second, err := s.SaveProtection(a.ID, 1, false, `{"title":"已发布"}`, &reviewed)
	require.NoError(t, err)
	require.Equal(t, int64(2), second.Revision)
	require.False(t, second.Uncertain)
	require.Equal(t, `{"title":"已发布"}`, second.LastPublished)

	loaded, err := s.LoadProtection(a.ID)
	require.NoError(t, err)
	require.Equal(t, second.Revision, loaded.Revision)
	require.Equal(t, second.LastPublished, loaded.LastPublished)
	require.False(t, loaded.Uncertain)
	require.True(t, loaded.ReviewedRevision.Valid)
	require.Equal(t, int64(1), loaded.ReviewedRevision.Int64)
}

func TestWorkspaceRequiresAliveAccount(t *testing.T) {
	s := openStore(t, filepath.Join(t.TempDir(), "accounts"))
	a := s.List()[0]
	require.NoError(t, s.Delete(a.ID))
	_, _, err := s.LoadDraft(a.ID)
	require.True(t, errors.Is(err, ErrNotFound))
	_, err = s.SaveProtection(a.ID, 0, false, "", nil)
	require.True(t, errors.Is(err, ErrNotFound))
}

func TestPublishProtectionCannotUnlockActiveTask(t *testing.T) {
	for _, running := range []bool{false, true} {
		name := "queued"
		if running {
			name = "running"
		}
		t.Run(name, func(t *testing.T) {
			s, a := publishTaskStore(t)
			ctx := context.Background()
			reviewed := int64(0)
			before, err := s.SaveProtection(a.ID, 0, false, "older-published", &reviewed)
			require.NoError(t, err)
			task, err := s.EnqueuePublishTask(ctx, publishTaskInput(a.ID, "unlock-guard"), before.Revision)
			require.NoError(t, err)
			if running {
				_, err = s.StartPublishTask(ctx, task.ID)
				require.NoError(t, err)
			}
			locked, err := s.LoadProtection(a.ID)
			require.NoError(t, err)
			require.Equal(t, task.ID, locked.ActiveTaskID)
			require.False(t, locked.ReviewedRevision.Valid)
			reviewed = locked.Revision
			_, err = s.SaveProtection(a.ID, locked.Revision, false, "overwritten", &reviewed)
			require.ErrorIs(t, err, ErrTaskActive)
			_, err = s.SaveProtection(a.ID, 0, false, "overwritten", nil)
			require.ErrorIs(t, err, ErrTaskActive, "活动任务错误优先于版本冲突")
			after, err := s.LoadProtection(a.ID)
			require.NoError(t, err)
			require.Equal(t, locked, after)
			_, err = s.FinishPublishTask(ctx, task.ID, TaskOutcome{Status: TaskUncertain})
			require.NoError(t, err)
			uncertain, err := s.LoadProtection(a.ID)
			require.NoError(t, err)
			require.Empty(t, uncertain.ActiveTaskID)
			require.True(t, uncertain.Uncertain)
			require.Equal(t, "older-published", uncertain.LastPublished)
			reviewed = uncertain.Revision
			unlocked, err := s.SaveProtection(a.ID, uncertain.Revision, false, uncertain.LastPublished, &reviewed)
			require.NoError(t, err)
			require.False(t, unlocked.Uncertain)
			require.Equal(t, uncertain.Revision+1, unlocked.Revision)
			require.True(t, unlocked.ReviewedRevision.Valid)
			require.Equal(t, reviewed, unlocked.ReviewedRevision.Int64)
			_, err = s.EnqueuePublishTask(ctx, publishTaskInput(a.ID, "after-review"), unlocked.Revision)
			require.NoError(t, err)
		})
	}
}

func TestSaveProtectionAndEnqueueAreAtomic(t *testing.T) {
	s, a := publishTaskStore(t)
	ctx := context.Background()
	start := make(chan struct{})
	enqueued := make(chan error, 1)
	saved := make(chan error, 1)
	go func() {
		<-start
		_, err := s.EnqueuePublishTask(ctx, publishTaskInput(a.ID, "protection-race"), 0)
		enqueued <- err
	}()
	go func() {
		<-start
		_, err := s.SaveProtection(a.ID, 0, false, "manual-save", nil)
		saved <- err
	}()
	close(start)
	enqueueErr, saveErr := <-enqueued, <-saved
	protection, err := s.LoadProtection(a.ID)
	require.NoError(t, err)
	require.Equal(t, int64(1), protection.Revision)
	if enqueueErr == nil {
		require.ErrorIs(t, saveErr, ErrTaskActive)
		require.True(t, protection.Uncertain)
		require.NotEmpty(t, protection.ActiveTaskID)
	} else {
		require.ErrorIs(t, enqueueErr, ErrTaskProtectionConflict)
		require.NoError(t, saveErr)
		require.False(t, protection.Uncertain)
		require.Empty(t, protection.ActiveTaskID)
		require.Equal(t, "manual-save", protection.LastPublished)
	}
}
