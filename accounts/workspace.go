package accounts

import (
	"database/sql"
	"errors"
	"time"
)

// ErrRevisionConflict 表示客户端基于旧版本保存，需先读取最新记录。
var ErrRevisionConflict = errors.New("revision conflict")

// Draft 是某个账号在服务端保存的草稿快照。
type Draft struct {
	Payload   []byte
	Revision  int64
	UpdatedAt time.Time
}

// PublishProtection 是跨浏览器共享的发布防重记录。
type PublishProtection struct {
	Revision         int64
	Uncertain        bool
	LastPublished    string
	ReviewedRevision sql.NullInt64
	UpdatedAt        time.Time
}

// LoadDraft 返回草稿；不存在时 found 为 false。
func (s *Store) LoadDraft(accountID string) (Draft, bool, error) {
	if _, err := s.Get(accountID); err != nil {
		return Draft{}, false, err
	}
	var (
		payload   string
		revision  int64
		updatedAt int64
	)
	err := s.db.QueryRow(
		`SELECT payload, revision, updated_at FROM drafts WHERE account_id = ?`, accountID,
	).Scan(&payload, &revision, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Draft{}, false, nil
	}
	if err != nil {
		return Draft{}, false, err
	}
	return Draft{Payload: []byte(payload), Revision: revision, UpdatedAt: time.UnixMilli(updatedAt).UTC()}, true, nil
}

// SaveDraft 以 baseRevision 做乐观并发控制，成功后版本加一。
func (s *Store) SaveDraft(accountID string, payload []byte, baseRevision int64) (Draft, error) {
	if _, err := s.Get(accountID); err != nil {
		return Draft{}, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Draft{}, err
	}
	defer tx.Rollback()
	var current int64
	err = tx.QueryRow(`SELECT revision FROM drafts WHERE account_id = ?`, accountID).Scan(&current)
	now := time.Now().UTC().Truncate(time.Millisecond)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if baseRevision != 0 {
			return Draft{}, ErrRevisionConflict
		}
		if _, err := tx.Exec(
			`INSERT INTO drafts (account_id, payload, revision, updated_at) VALUES (?, ?, 1, ?)`,
			accountID, string(payload), now.UnixMilli(),
		); err != nil {
			return Draft{}, err
		}
		current = 1
	case err != nil:
		return Draft{}, err
	default:
		if baseRevision != current {
			return Draft{}, ErrRevisionConflict
		}
		result, err := tx.Exec(
			`UPDATE drafts SET payload = ?, revision = ?, updated_at = ? WHERE account_id = ? AND revision = ?`,
			string(payload), current+1, now.UnixMilli(), accountID, current,
		)
		if err != nil {
			return Draft{}, err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return Draft{}, err
		}
		if affected == 0 {
			return Draft{}, ErrRevisionConflict
		}
		current++
	}
	if err := tx.Commit(); err != nil {
		return Draft{}, err
	}
	return Draft{Payload: payload, Revision: current, UpdatedAt: now}, nil
}

// DraftRevision 返回当前草稿版本；不存在时为 0。
func (s *Store) DraftRevision(accountID string) (int64, error) {
	var revision int64
	err := s.db.QueryRow(`SELECT revision FROM drafts WHERE account_id = ?`, accountID).Scan(&revision)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return revision, err
}

func (s *Store) DeleteDraft(accountID string) error {
	if _, err := s.Get(accountID); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM drafts WHERE account_id = ?`, accountID)
	return err
}

// LoadProtection 返回发布防重记录；不存在时返回 revision 0。
func (s *Store) LoadProtection(accountID string) (PublishProtection, error) {
	if _, err := s.Get(accountID); err != nil {
		return PublishProtection{}, err
	}
	var (
		protection       PublishProtection
		uncertain        int
		updatedAt        int64
		reviewedRevision sql.NullInt64
	)
	err := s.db.QueryRow(
		`SELECT revision, uncertain, last_published, reviewed_revision, updated_at
		 FROM publish_protection WHERE account_id = ?`, accountID,
	).Scan(&protection.Revision, &uncertain, &protection.LastPublished, &reviewedRevision, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return PublishProtection{}, nil
	}
	if err != nil {
		return PublishProtection{}, err
	}
	protection.Uncertain = uncertain != 0
	protection.ReviewedRevision = reviewedRevision
	protection.UpdatedAt = time.UnixMilli(updatedAt).UTC()
	return protection, nil
}

// SaveProtection 以 baseRevision 条件更新发布防重记录。
func (s *Store) SaveProtection(accountID string, baseRevision int64, uncertain bool, lastPublished string, reviewedRevision *int64) (PublishProtection, error) {
	if _, err := s.Get(accountID); err != nil {
		return PublishProtection{}, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return PublishProtection{}, err
	}
	defer tx.Rollback()
	var current int64
	err = tx.QueryRow(`SELECT revision FROM publish_protection WHERE account_id = ?`, accountID).Scan(&current)
	now := time.Now().UTC().Truncate(time.Millisecond)
	reviewed := any(nil)
	if reviewedRevision != nil {
		reviewed = *reviewedRevision
	}
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if baseRevision != 0 {
			return PublishProtection{}, ErrRevisionConflict
		}
		if _, err := tx.Exec(
			`INSERT INTO publish_protection
			 (account_id, revision, uncertain, last_published, reviewed_revision, updated_at)
			 VALUES (?, 1, ?, ?, ?, ?)`,
			accountID, boolInt(uncertain), lastPublished, reviewed, now.UnixMilli(),
		); err != nil {
			return PublishProtection{}, err
		}
		current = 1
	case err != nil:
		return PublishProtection{}, err
	default:
		if baseRevision != current {
			return PublishProtection{}, ErrRevisionConflict
		}
		result, err := tx.Exec(
			`UPDATE publish_protection
			 SET revision = ?, uncertain = ?, last_published = ?, reviewed_revision = ?, updated_at = ?
			 WHERE account_id = ? AND revision = ?`,
			current+1, boolInt(uncertain), lastPublished, reviewed, now.UnixMilli(), accountID, current,
		)
		if err != nil {
			return PublishProtection{}, err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return PublishProtection{}, err
		}
		if affected == 0 {
			return PublishProtection{}, ErrRevisionConflict
		}
		current++
	}
	if err := tx.Commit(); err != nil {
		return PublishProtection{}, err
	}
	protection := PublishProtection{
		Revision: current, Uncertain: uncertain, LastPublished: lastPublished, UpdatedAt: now,
	}
	if reviewedRevision != nil {
		protection.ReviewedRevision = sql.NullInt64{Int64: *reviewedRevision, Valid: true}
	}
	return protection, nil
}
