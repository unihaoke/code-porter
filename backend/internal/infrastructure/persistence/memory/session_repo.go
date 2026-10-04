package memory

import (
	"context"
	"sync"
	"time"

	"github.com/codeporter/code-porter/internal/domain/user"
)

// SessionRepository 会话仓储的内存实现。
type SessionRepository struct {
	mu sync.RWMutex
	m  map[string]*user.Session // key: tokenHash
}

// NewSessionRepository 构造仓储。
func NewSessionRepository() *SessionRepository {
	return &SessionRepository{m: make(map[string]*user.Session)}
}

// Save 创建或更新会话。
func (r *SessionRepository) Save(_ context.Context, s *user.Session) error {
	if s == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.m[s.TokenHash()] = s.Clone()
	return nil
}

// FindByTokenHash 查询会话。
func (r *SessionRepository) FindByTokenHash(_ context.Context, tokenHash string) (*user.Session, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.m[tokenHash]
	if !ok {
		return nil, user.ErrSessionNotFound
	}
	return s.Clone(), nil
}

// Delete 删除会话。
func (r *SessionRepository) Delete(_ context.Context, tokenHash string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.m, tokenHash)
	return nil
}

// DeleteByUserExcept 删除某用户全部会话，exceptHash 非空时保留该会话。
func (r *SessionRepository) DeleteByUserExcept(_ context.Context, userID user.ID, exceptHash string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for hash, s := range r.m {
		if s.UserID() == userID && hash != exceptHash {
			delete(r.m, hash)
		}
	}
	return nil
}

// DeleteExpired 清理过期会话。
func (r *SessionRepository) DeleteExpired(_ context.Context, now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for hash, s := range r.m {
		if s.IsExpired(now) {
			delete(r.m, hash)
		}
	}
	return nil
}
