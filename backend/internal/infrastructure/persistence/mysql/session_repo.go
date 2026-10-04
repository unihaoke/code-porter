package mysql

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/codeporter/code-porter/internal/domain/user"
)

// SessionRepository 会话仓储的 MySQL 实现。
type SessionRepository struct {
	db *sql.DB
}

// NewSessionRepository 构造仓储。
func NewSessionRepository(db *sql.DB) *SessionRepository { return &SessionRepository{db: db} }

// Save 创建或刷新会话。
func (r *SessionRepository) Save(ctx context.Context, s *user.Session) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO sessions (token_hash, user_id, created_at, expires_at, last_seen_at)
		VALUES (?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			user_id=VALUES(user_id), expires_at=VALUES(expires_at), last_seen_at=VALUES(last_seen_at)`,
		s.TokenHash(), string(s.UserID()), s.CreatedAt(), s.ExpiresAt(), s.LastSeenAt())
	return err
}

// FindByTokenHash 查询会话。
func (r *SessionRepository) FindByTokenHash(ctx context.Context, tokenHash string) (*user.Session, error) {
	var (
		hash, uid                                           string
		createdAt, expiresAt, lastSeen                      time.Time
	)
	err := r.db.QueryRowContext(ctx, `
		SELECT token_hash, user_id, created_at, expires_at, last_seen_at
		FROM sessions WHERE token_hash = ?`, tokenHash).
		Scan(&hash, &uid, &createdAt, &expiresAt, &lastSeen)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, user.ErrSessionNotFound
		}
		return nil, err
	}
	return user.RewriteSession(hash, user.ID(uid), createdAt, expiresAt, lastSeen), nil
}

// Delete 删除会话。
func (r *SessionRepository) Delete(ctx context.Context, tokenHash string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, tokenHash)
	return err
}

// DeleteByUserExcept 删除某用户全部会话，exceptHash 非空时保留。
func (r *SessionRepository) DeleteByUserExcept(ctx context.Context, userID user.ID, exceptHash string) error {
	if exceptHash == "" {
		_, err := r.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, string(userID))
		return err
	}
	_, err := r.db.ExecContext(ctx,
		`DELETE FROM sessions WHERE user_id = ? AND token_hash <> ?`, string(userID), exceptHash)
	return err
}

// DeleteExpired 清理过期会话。
func (r *SessionRepository) DeleteExpired(ctx context.Context, now time.Time) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, now)
	return err
}
