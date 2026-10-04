package memory

import (
	"context"
	"sort"
	"sync"

	"github.com/codeporter/code-porter/internal/domain/user"
)

// UserRepository 用户仓储的内存实现（测试与本地装配使用）。
type UserRepository struct {
	mu       sync.RWMutex
	byID     map[user.ID]*user.User
	byName   map[string]user.ID
}

// NewUserRepository 构造仓储。
func NewUserRepository() *UserRepository {
	return &UserRepository{byID: make(map[user.ID]*user.User), byName: make(map[string]user.ID)}
}

// Save 新建或更新；用户名重复返回 user.ErrDuplicateUsername。
func (r *UserRepository) Save(_ context.Context, u *user.User) error {
	if u == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.byName[u.Username()]; ok && existing != u.ID() {
		return user.ErrDuplicateUsername
	}
	if old, ok := r.byID[u.ID()]; ok && old.Username() != u.Username() {
		delete(r.byName, old.Username())
	}
	r.byID[u.ID()] = u.Clone()
	r.byName[u.Username()] = u.ID()
	return nil
}

// FindByID 按 ID 查询。
func (r *UserRepository) FindByID(_ context.Context, id user.ID) (*user.User, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	u, ok := r.byID[id]
	if !ok {
		return nil, user.ErrUserNotFound
	}
	return u.Clone(), nil
}

// FindByUsername 按用户名查询。
func (r *UserRepository) FindByUsername(_ context.Context, username string) (*user.User, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	id, ok := r.byName[user.NormalizeUsername(username)]
	if !ok {
		return nil, user.ErrUserNotFound
	}
	return r.byID[id].Clone(), nil
}

// List 列出全部用户（按创建时间升序）。
func (r *UserRepository) List(_ context.Context) ([]*user.User, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*user.User, 0, len(r.byID))
	for _, u := range r.byID {
		out = append(out, u.Clone())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt().Before(out[j].CreatedAt()) })
	return out, nil
}

// Count 用户总数。
func (r *UserRepository) Count(_ context.Context) (int64, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return int64(len(r.byID)), nil
}

// Delete 删除用户（内存级联由各仓储自行约束；测试装配时子仓储独立清理）。
func (r *UserRepository) Delete(_ context.Context, id user.ID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if u, ok := r.byID[id]; ok {
		delete(r.byName, u.Username())
	}
	delete(r.byID, id)
	return nil
}
