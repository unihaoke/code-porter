package mysql

import (
	"context"
	"database/sql"
	"errors"

	"github.com/codeporter/code-porter/internal/domain/agent"
	"github.com/codeporter/code-porter/internal/domain/user"
)

// AgentRepository Agent 身份仓储的 MySQL 实现（只存身份，不存运行时态）。
type AgentRepository struct {
	db *sql.DB
}

// NewAgentRepository 构造仓储。
func NewAgentRepository(db *sql.DB) *AgentRepository { return &AgentRepository{db: db} }

// Save upsert 实例身份。
func (r *AgentRepository) Save(ctx context.Context, a *agent.Agent) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO agents (id, user_id, name, created_at, last_seen_at)
		VALUES (?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			name=VALUES(name), last_seen_at=VALUES(last_seen_at)`,
		string(a.ID()), string(a.OwnerID()), a.Name(), a.RegisteredAt(), a.LastHeartbeatAt())
	return err
}

// Find 按 ID 查询。
func (r *AgentRepository) Find(ctx context.Context, id agent.ID) (*agent.Agent, error) {
	return r.findOne(ctx, `WHERE id = ?`, string(id))
}

// FindByIdentity 按归属 + 实例 ID 查询；不匹配返回 ErrAgentNotFound。
func (r *AgentRepository) FindByIdentity(ctx context.Context, ownerID user.ID, id agent.ID) (*agent.Agent, error) {
	return r.findOne(ctx, `WHERE id = ? AND user_id = ?`, string(id), string(ownerID))
}

func (r *AgentRepository) findOne(ctx context.Context, tail string, args ...any) (*agent.Agent, error) {
	var id, uid, name string
	var createdAt, lastSeen sql.NullTime
	err := r.db.QueryRowContext(ctx, `
		SELECT id, user_id, name, created_at, last_seen_at FROM agents `+tail, args...).
		Scan(&id, &uid, &name, &createdAt, &lastSeen)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, agent.ErrAgentNotFound
		}
		return nil, err
	}
	a := &agent.Agent{}
	a.RewriteIdentity(agent.ID(id), user.ID(uid), name, createdAt.Time, lastSeen.Time)
	return a, nil
}

// ListByOwner 列出某用户的全部实例。
func (r *AgentRepository) ListByOwner(ctx context.Context, ownerID user.ID) ([]*agent.Agent, error) {
	return r.query(ctx, `WHERE user_id = ? ORDER BY created_at ASC`, string(ownerID))
}

// List 列出全部实例。
func (r *AgentRepository) List(ctx context.Context) ([]*agent.Agent, error) {
	return r.query(ctx, `ORDER BY created_at ASC`)
}

func (r *AgentRepository) query(ctx context.Context, tail string, args ...any) ([]*agent.Agent, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, user_id, name, created_at, last_seen_at FROM agents `+tail, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*agent.Agent
	for rows.Next() {
		var id, uid, name string
		var createdAt, lastSeen sql.NullTime
		if err := rows.Scan(&id, &uid, &name, &createdAt, &lastSeen); err != nil {
			return nil, err
		}
		a := &agent.Agent{}
		a.RewriteIdentity(agent.ID(id), user.ID(uid), name, createdAt.Time, lastSeen.Time)
		out = append(out, a)
	}
	return out, rows.Err()
}

// Delete 删除实例身份。
func (r *AgentRepository) Delete(ctx context.Context, id agent.ID) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM agents WHERE id = ?`, string(id))
	return err
}
