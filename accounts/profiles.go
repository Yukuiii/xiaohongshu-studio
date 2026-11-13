package accounts

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// NewCandidate 调用前必须确认使用原 profile 的浏览器已经退出。
func (s *Store) NewCandidate(id string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, err := s.get(id)
	if err != nil {
		return "", err
	}
	if err := s.profileDirectory(id, a.ActiveProfile); err != nil {
		return "", err
	}
	profileID, err := newID()
	if err != nil {
		return "", err
	}
	target := s.profilePath(id, profileID)
	if err := os.Mkdir(target, 0700); err != nil {
		return "", err
	}
	if err := copyProfile(s.profilePath(id, a.ActiveProfile), target); err != nil {
		_ = os.RemoveAll(target)
		return "", err
	}
	return profileID, nil
}

// CommitProfile 仅在候选浏览器退出、身份确认后切换指针。
func (s *Store) CommitProfile(id, profileID, userID, nickname string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, err := s.get(id)
	if err != nil {
		return err
	}
	if !validID(profileID) {
		return fmt.Errorf("%w: profile id", ErrInvalid)
	}
	if err := s.checkIdentity(a, userID); err != nil {
		return err
	}
	if err := s.profileDirectory(id, profileID); err != nil {
		return err
	}
	a.ActiveProfile, a.UserID, a.Nickname = profileID, userID, nickname
	a.setLoginStatus(true)
	return s.save(a)
}

func (s *Store) DiscardProfile(id, profileID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, err := s.get(id)
	if err != nil {
		return err
	}
	if !validID(profileID) || profileID == a.ActiveProfile {
		return fmt.Errorf("%w: cannot discard active or invalid profile", ErrInvalid)
	}
	if err := s.profileParent(id); err != nil {
		return err
	}
	return os.RemoveAll(s.profilePath(id, profileID))
}

// ResetProfile 切换到空目录后清理旧数据，保留账号、seed 和绑定身份。
func (s *Store) ResetProfile(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, err := s.get(id)
	if err != nil {
		return err
	}
	if err := s.profileParent(id); err != nil {
		return err
	}
	profileID, err := newID()
	if err != nil {
		return err
	}
	target := s.profilePath(id, profileID)
	if err := os.Mkdir(target, 0700); err != nil {
		return err
	}
	a.ActiveProfile = profileID
	a.LegacyImportPending = false
	a.setLoginStatus(false)
	if err := s.save(a); err != nil {
		_ = os.RemoveAll(target)
		return err
	}
	entries, err := os.ReadDir(filepath.Dir(target))
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() != profileID {
			if err := os.RemoveAll(filepath.Join(filepath.Dir(target), entry.Name())); err != nil {
				return err
			}
		}
	}
	return removeFile(s.pendingPath(id))
}

func (s *Store) profileParent(id string) error {
	if err := directory(filepath.Join(s.root, id)); err != nil {
		return err
	}
	return directory(filepath.Join(s.root, id, "profiles"))
}

func (s *Store) profileDirectory(id, profileID string) error {
	if err := s.profileParent(id); err != nil {
		return err
	}
	return directory(s.profilePath(id, profileID))
}

func copyProfile(source, target string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == source {
			return nil
		}
		if transientProfileFile(entry.Name()) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		// 未知符号链接和特殊文件一律拒绝，避免复制到账号目录之外。
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: profile symlink: %s", ErrInvalid, path)
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(target, relative)
		if entry.IsDir() {
			return os.Mkdir(destination, 0700)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%w: special profile file: %s", ErrInvalid, path)
		}
		return copyProfileFile(path, destination, info)
	})
}

func transientProfileFile(name string) bool {
	return strings.HasPrefix(name, "Singleton") || name == "DevToolsActivePort" || strings.HasPrefix(name, ".org.chromium.Chromium.")
}

func copyProfileFile(source, target string, info fs.FileInfo) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm()&0700)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	if copyErr == nil {
		copyErr = output.Sync()
	}
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Chtimes(target, info.ModTime(), info.ModTime())
}
