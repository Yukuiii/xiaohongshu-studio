package accounts

import (
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
