package mysql

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/codeporter/code-porter/internal/domain/apikey"
	"github.com/codeporter/code-porter/internal/domain/user"
)

// APIKeyRepository 秘钥仓储的 MySQL 实现。
type APIKeyRepository struct {
	db *sql.DB
}

// NewAPIKeyRepository 构造仓储。
func NewAPIKeyRepository(db *sql.DB) *APIKeyRepository { return &APIKeyRepository{db: db} }

// Save 新建或更新秘钥。
func (r *APIKeyRepository) Save(ctx context.Context, k *apikey.APIKey) error {
	var expiresAt any
	if k.HasExpiry() {
		t := k.ExpiresAt()
		expiresAt = t
	}
	var lastUsed sql.NullTime
	if !k.LastUsedAt().IsZero() {
		lastUsed = sql.NullTime{Time: k.LastUsedAt(), Valid: true}
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO api_keys (id, user_id, name, key_hash, prefix, scopes, expires_at, last_used_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			name=VALUES(name), scopes=VALUES(scopes), expires_at=VALUES(expires_at),
			last_used_at=VALUES(last_used_at), updated_at=VALUES(updated_at)`,
		string(k.ID()), string(k.OwnerID()), k.Name(), k.KeyHash(), k.Prefix(),
		encodeScopes(k.Scopes()), expiresAt, lastUsed, k.CreatedAt(), k.UpdatedAt())
	return err
}

// FindByHash 按明文哈希查询。
func (r *APIKeyRepository) FindByHash(ctx context.Context, keyHash string) (*apikey.APIKey, error) {
	return r.findOne(ctx, `WHERE key_hash = ?`, keyHash)
}

// FindByID 按 ID 查询。
func (r *APIKeyRepository) FindByID(ctx context.Context, id apikey.ID) (*apikey.APIKey, error) {
	return r.findOne(ctx, `WHERE id = ?`, string(id))
}

func (r *APIKeyRepository) findOne(ctx context.Context, where string, arg any) (*apikey.APIKey, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, user_id, name, key_hash, prefix, scopes, expires_at, last_used_at, created_at, updated_at
		FROM api_keys `+where, arg)
	k, err := scanAPIKey(row)
	if err != nil {
		return nil, err
	}
	return k, nil
}

// ListByUser 列出某用户的秘钥（按创建时间升序）。
func (r *APIKeyRepository) ListByUser(ctx context.Context, ownerID user.ID) ([]*apikey.APIKey, error) {
	return r.query(ctx, `WHERE user_id = ? ORDER BY created_at ASC`, string(ownerID))
}

// ListAll 列出全部秘钥。
func (r *APIKeyRepository) ListAll(ctx context.Context) ([]*apikey.APIKey, error) {
	return r.query(ctx, `ORDER BY created_at ASC`)
}

func (r *APIKeyRepository) query(ctx context.Context, tail string, args ...any) ([]*apikey.APIKey, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, user_id, name, key_hash, prefix, scopes, expires_at, last_used_at, created_at, updated_at
		FROM api_keys `+tail, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*apikey.APIKey
	for rows.Next() {
		k, err := scanAPIKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// Delete 硬删除秘钥（删除即吊销）。
func (r *APIKeyRepository) Delete(ctx context.Context, id apikey.ID) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM api_keys WHERE id = ?`, string(id))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return apikey.ErrAPIKeyNotFound
	}
	return nil
}

func scanAPIKey(s rowScanner) (*apikey.APIKey, error) {
	var (
		id, userID, name, keyHash, prefix, scopesCSV string
		expiresAt                                    sql.NullTime
		lastUsed                                     sql.NullTime
		createdAt, updatedAt                         time.Time
	)
	if err := s.Scan(&id, &userID, &name, &keyHash, &prefix, &scopesCSV,
		&expiresAt, &lastUsed, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, apikey.ErrAPIKeyNotFound
		}
		return nil, err
	}
	k := &apikey.APIKey{}
	var expires *time.Time
	if expiresAt.Valid {
		t := expiresAt.Time
		expires = &t
	}
	k.Rewrite(apikey.ID(id), user.ID(userID), name, decodeScopes(scopesCSV),
		keyHash, prefix, expires, lastUsed.Time, createdAt, updatedAt)
	return k, nil
}

// encodeScopes 以固定顺序（agent,api）落库，保证读取顺序稳定。
func encodeScopes(in []apikey.Scope) string {
	weight := map[apikey.Scope]int{apikey.ScopeAgent: 0, apikey.ScopeAPI: 1}
	out := append([]apikey.Scope(nil), in...)
	sort.SliceStable(out, func(i, j int) bool { return weight[out[i]] < weight[out[j]] })
	parts := make([]string, 0, len(out))
	for _, s := range out {
		parts = append(parts, string(s))
	}
	return strings.Join(parts, ",")
}

// decodeScopes 解析 CSV scopes，非法值忽略（仓储不做业务校验，鉴权方按空集合拒绝）。
func decodeScopes(csv string) []apikey.Scope {
	if strings.TrimSpace(csv) == "" {
		return nil
	}
	parts := strings.Split(csv, ",")
	out := make([]apikey.Scope, 0, len(parts))
	for _, p := range parts {
		s := apikey.Scope(strings.TrimSpace(p))
		if s.Valid() {
			out = append(out, s)
		}
	}
	return out
}
