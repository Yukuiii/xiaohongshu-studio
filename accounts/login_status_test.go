package accounts

import (
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoginStatusLoadsLegacyMetadataAndPersists(t *testing.T) {
	root := filepath.Join(t.TempDir(), "accounts")
	s := openStore(t, root, "", 0)
	a := s.List()[0]
	require.NoError(t, s.BindIdentity(a.ID, "user-a", "账号 A"))
	path := filepath.Join(root, a.ID, "account.json")
	var legacy map[string]any
	require.NoError(t, json.Unmarshal(readTestFile(t, path), &legacy))
	delete(legacy, "is_logged_in")
	delete(legacy, "login_checked_at")
	data, err := json.Marshal(legacy)
	require.NoError(t, err)
	writeTestFile(t, path, data)
	require.NoError(t, s.Close())

	s = openStore(t, root, "", 0)
	a, err = s.Get(a.ID)
	require.NoError(t, err)
	require.False(t, a.IsLoggedIn, "历史绑定身份不能当作当前登录有效")
	require.Nil(t, a.LoginCheckedAt)
	require.NoError(t, s.BindIdentity(a.ID, "user-a", "账号 A"))
	checked, err := s.Get(a.ID)
	require.NoError(t, err)
	require.True(t, checked.IsLoggedIn)
	require.NotNil(t, checked.LoginCheckedAt)
	require.NoError(t, s.Close())

	s = openStore(t, root, "", 0)
	restarted, err := s.Get(a.ID)
	require.NoError(t, err)
	require.Equal(t, checked, restarted)
	require.NoError(t, s.SetLoginStatus(a.ID, false))
	require.NoError(t, s.Close())
	s = openStore(t, root, "", 0)
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
