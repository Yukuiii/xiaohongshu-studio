package accounts

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoginStatusDefaultsAndPersists(t *testing.T) {
	root := filepath.Join(t.TempDir(), "accounts")
	s := openStore(t, root)
	a := s.List()[0]
	require.False(t, a.IsLoggedIn)
	require.False(t, a.VerificationRequired)
	require.Nil(t, a.LoginCheckedAt, "新账号没有检查记录")
	require.NoError(t, s.BindIdentity(a.ID, "user-a", "账号 A"))
	checked, err := s.Get(a.ID)
	require.NoError(t, err)
	require.True(t, checked.IsLoggedIn)
	require.NotNil(t, checked.LoginCheckedAt)
	require.NoError(t, s.Close())

	s = openStore(t, root)
	restarted, err := s.Get(a.ID)
	require.NoError(t, err)
	require.Equal(t, checked, restarted)
	require.NoError(t, s.SetLoginStatus(a.ID, false))
	require.NoError(t, s.Close())
	s = openStore(t, root)
	restarted, err = s.Get(a.ID)
	require.NoError(t, err)
	require.False(t, restarted.IsLoggedIn)
	require.NotNil(t, restarted.LoginCheckedAt)
	require.Equal(t, "user-a", restarted.UserID)
}

func TestLoginStatusKeepsConcurrentAccountChanges(t *testing.T) {
	s := openStore(t, filepath.Join(t.TempDir(), "accounts"), "", 0)
	a := s.List()[0]
	require.NoError(t, s.BindIdentity(a.ID, "user-a", "昵称"))
	var wg sync.WaitGroup
	failures := make(chan error, 2)
	wg.Go(func() {
		for range 10 {
			if _, err := s.Rename(a.ID, "新的备注"); err != nil {
				failures <- err
				return
			}
		}
	})
	wg.Go(func() {
		for range 10 {
			if err := s.SetLoginStatus(a.ID, false); err != nil {
				failures <- err
				return
			}
		}
	})
	wg.Wait()
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}
	current, err := s.Get(a.ID)
	require.NoError(t, err)
	require.Equal(t, "新的备注", current.Name)
	require.Equal(t, "昵称", current.Nickname)
	require.Equal(t, "user-a", current.UserID)
	require.False(t, current.IsLoggedIn)
	require.NotNil(t, current.LoginCheckedAt)
	require.NoError(t, s.Delete(a.ID))
	require.ErrorIs(t, s.SetLoginStatus(a.ID, true), ErrNotFound)
}

func TestVerificationRequiredPersistsWithoutChangingIdentity(t *testing.T) {
	for _, loggedIn := range []bool{false, true} {
		name := "logged_out"
		if loggedIn {
			name = "logged_in"
		}
		t.Run(name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "accounts")
			s := openStore(t, root)
			a := s.List()[0]
			require.NoError(t, s.BindIdentity(a.ID, "user-a", "账号 A"))
			require.NoError(t, s.SetAvatar(a.ID, "https://sns-avatar-qc.xhscdn.com/avatar.jpg"))
			require.NoError(t, s.SetLoginStatus(a.ID, loggedIn))
			a, err := s.Get(a.ID)
			require.NoError(t, err)
			profileFile := filepath.Join(s.ProfilePath(a), "Default", "Preferences")
			writeTestFile(t, profileFile, []byte("existing profile"))
			other, err := s.Create("账号 B")
			require.NoError(t, err)

			require.NoError(t, s.SetVerificationRequired(a.ID))
			marked, err := s.Get(a.ID)
			require.NoError(t, err)
			require.True(t, marked.VerificationRequired)
			require.Equal(t, loggedIn, marked.IsLoggedIn)
			require.NotNil(t, marked.LoginCheckedAt)
			require.False(t, marked.LoginCheckedAt.Before(*a.LoginCheckedAt))
			expected := a
			expected.VerificationRequired = true
			expected.LoginCheckedAt = marked.LoginCheckedAt
			expected.UpdatedAt = marked.UpdatedAt
			require.Equal(t, expected, marked)
			require.NoError(t, s.Close())
			require.NoError(t, s.db.Close())

			s = openStore(t, root)
			restarted, err := s.Get(a.ID)
			require.NoError(t, err)
			require.Equal(t, marked, restarted)
			require.Equal(t, "existing profile", string(readTestFile(t, profileFile)))
			untouched, err := s.Get(other.ID)
			require.NoError(t, err)
			require.Equal(t, other, untouched)
		})
	}
}

func TestVerificationRequiredClearsOnExplicitLoginResult(t *testing.T) {
	tests := []struct {
		name     string
		loggedIn bool
		apply    func(*Store, string) error
	}{
		{"login_success", true, func(s *Store, id string) error { return s.SetLoginStatus(id, true) }},
		{"login_expired", false, func(s *Store, id string) error { return s.SetLoginStatus(id, false) }},
		{"bind_identity", true, func(s *Store, id string) error { return s.BindIdentity(id, "user-a", "账号 A") }},
		{"commit_profile", true, func(s *Store, id string) error {
			profileID, err := s.NewCandidate(id)
			if err != nil {
				return err
			}
			return s.CommitProfile(id, profileID, "user-a", "账号 A")
		}},
		{"reset_profile", false, func(s *Store, id string) error { return s.ResetProfile(id) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "accounts")
			s := openStore(t, root)
			a := s.List()[0]
			require.NoError(t, s.BindIdentity(a.ID, "user-a", "账号 A"))
			require.NoError(t, s.SetVerificationRequired(a.ID))
			other, err := s.Create("账号 B")
			require.NoError(t, err)
			require.NoError(t, s.SetVerificationRequired(other.ID))
			other, err = s.Get(other.ID)
			require.NoError(t, err)

			require.NoError(t, tt.apply(s, a.ID))
			require.NoError(t, s.Close())
			require.NoError(t, s.db.Close())
			s = openStore(t, root)
			current, err := s.Get(a.ID)
			require.NoError(t, err)
			require.False(t, current.VerificationRequired)
			require.Equal(t, tt.loggedIn, current.IsLoggedIn)
			require.Equal(t, "user-a", current.UserID)
			require.Equal(t, "账号 A", current.Nickname)
			require.Equal(t, a.Seed, current.Seed)
			require.NotNil(t, current.LoginCheckedAt)
			untouched, err := s.Get(other.ID)
			require.NoError(t, err)
			require.Equal(t, other, untouched)
		})
	}
}

func TestVerificationRequiredClearsOnDelete(t *testing.T) {
	root := filepath.Join(t.TempDir(), "accounts")
	s := openStore(t, root)
	a := s.List()[0]
	require.NoError(t, s.BindIdentity(a.ID, "user-a", "账号 A"))
	require.NoError(t, s.SetVerificationRequired(a.ID))
	require.NoError(t, s.Delete(a.ID))
	require.False(t, s.accounts[a.ID].VerificationRequired)
	require.ErrorIs(t, s.SetVerificationRequired(a.ID), ErrNotFound)
	require.NoError(t, s.Close())
	require.NoError(t, s.db.Close())
	s = openEmptyStore(t, root)
	tombstone := s.accounts[a.ID]
	require.True(t, tombstone.Deleted)
	require.False(t, tombstone.VerificationRequired)
	require.False(t, tombstone.IsLoggedIn)
	require.Nil(t, tombstone.LoginCheckedAt)
}

func TestVerificationRequiredRejectsUnavailableAccount(t *testing.T) {
	s := openStore(t, filepath.Join(t.TempDir(), "accounts"))
	a := s.List()[0]
	require.ErrorIs(t, s.SetVerificationRequired("invalid"), ErrInvalid)
	require.ErrorIs(t, s.SetVerificationRequired("00000000000000000000000000000000"), ErrNotFound)
	require.NoError(t, s.Close())
	require.ErrorIs(t, s.SetVerificationRequired(a.ID), os.ErrClosed)
}

func TestVerificationRequiredKeepsCacheOnSaveFailure(t *testing.T) {
	s := openStore(t, filepath.Join(t.TempDir(), "accounts"))
	a := s.List()[0]
	require.NoError(t, s.db.Close())
	require.Error(t, s.SetVerificationRequired(a.ID))
	current, err := s.Get(a.ID)
	require.NoError(t, err)
	require.Equal(t, a, current)
}
