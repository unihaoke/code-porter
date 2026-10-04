package memory

import (
	"context"
	"sort"
	"sync"

	"github.com/codeporter/code-porter/internal/domain/apikey"
	"github.com/codeporter/code-porter/internal/domain/user"
)

// APIKeyRepository 秘钥仓储的内存实现。
type APIKeyRepository struct {
	mu     sync.RWMutex
	byID   map[apikey.ID]*apikey.APIKey
	byHash map[string]apikey.ID
}

// NewAPIKeyRepository 构造仓储。
func NewAPIKeyRepository() *APIKeyRepository {
	return &APIKeyRepository{
		byID:   make(map[apikey.ID]*apikey.APIKey),
		byHash: make(map[string]apikey.ID),
	}
}

// Save 新建或更新秘钥。
func (r *APIKeyRepository) Save(_ context.Context, k *apikey.APIKey) error {
	if k == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byID[k.ID()] = k.Clone()
	r.byHash[k.KeyHash()] = k.ID()
	return nil
}

// FindByHash 按哈希查询。
func (r *APIKeyRepository) FindByHash(_ context.Context, keyHash string) (*apikey.APIKey, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	id, ok := r.byHash[keyHash]
	if !ok {
		return nil, apikey.ErrAPIKeyNotFound
	}
	return r.byID[id].Clone(), nil
}

// FindByID 按 ID 查询。
func (r *APIKeyRepository) FindByID(_ context.Context, id apikey.ID) (*apikey.APIKey, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	k, ok := r.byID[id]
	if !ok {
		return nil, apikey.ErrAPIKeyNotFound
	}
	return k.Clone(), nil
}

// ListByUser 列出某用户秘钥（按创建时间升序）。
func (r *APIKeyRepository) ListByUser(_ context.Context, ownerID user.ID) ([]*apikey.APIKey, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*apikey.APIKey, 0)
	for _, k := range r.byID {
		if k.OwnerID() == ownerID {
			out = append(out, k.Clone())
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt().Before(out[j].CreatedAt()) })
	return out, nil
}

// ListAll 列出全部秘钥。
func (r *APIKeyRepository) ListAll(_ context.Context) ([]*apikey.APIKey, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*apikey.APIKey, 0, len(r.byID))
	for _, k := range r.byID {
		out = append(out, k.Clone())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt().Before(out[j].CreatedAt()) })
	return out, nil
}

// Delete 硬删除秘钥。
func (r *APIKeyRepository) Delete(_ context.Context, id apikey.ID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if k, ok := r.byID[id]; ok {
		delete(r.byHash, k.KeyHash())
	}
	delete(r.byID, id)
	return nil
}
