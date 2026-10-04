package mysql

import (
	"context"
	"database/sql"
	"errors"
	"time"

	mysqldrv "github.com/go-sql-driver/mysql"

	"github.com/codeporter/code-porter/internal/domain/user"
)

// UserRepository 用户仓储的 MySQL 实现。
type UserRepository struct {
	db *sql.DB
}

// NewUserRepository 构造仓储。
func NewUserRepository(db *sql.DB) *UserRepository { return &UserRepository{db: db} }

// Save 新建或更新用户；用户名撞唯一约束返回 ErrDuplicateUsername。
func (r *UserRepository) Save(ctx context.Context, u *user.User) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO users (id, username, password_hash, role, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			username=VALUES(username), password_hash=VALUES(password_hash),
			role=VALUES(role), status=VALUES(status), updated_at=VALUES(updated_at)`,
		string(u.ID()), u.Username(), u.PasswordHash(), string(u.Role()), string(u.Status()),
		u.CreatedAt(), u.UpdatedAt())
	if err != nil {
		if isDuplicateEntry(err) {
			return user.ErrDuplicateUsername
		}
		return err
	}
	return nil
}

// FindByID 按 ID 查询。
func (r *UserRepository) FindByID(ctx context.Context, id user.ID) (*user.User, error) {
	return r.findOne(ctx, `WHERE id = ?`, string(id))
}

// FindByUsername 按用户名查询。
func (r *UserRepository) FindByUsername(ctx context.Context, username string) (*user.User, error) {
	return r.findOne(ctx, `WHERE username = ?`, user.NormalizeUsername(username))
}

func (r *UserRepository) findOne(ctx context.Context, where string, arg any) (*user.User, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, username, password_hash, role, status, created_at, updated_at
		FROM users `+where, arg)
	return scanUser(row)
}

// List 列出全部用户（按创建时间升序）。
func (r *UserRepository) List(ctx context.Context) ([]*user.User, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, username, password_hash, role, status, created_at, updated_at
		FROM users ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*user.User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// Count 用户总数。
func (r *UserRepository) Count(ctx context.Context) (int64, error) {
	var n int64
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// Delete 删除用户；子表由外键 ON DELETE CASCADE 清理。
func (r *UserRepository) Delete(ctx context.Context, id user.ID) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, string(id))
	return err
}

// rowScanner 兼容 *sql.Row 与 *sql.Rows。
type rowScanner interface {
	Scan(dest ...any) error
}

func scanUser(s rowScanner) (*user.User, error) {
	var (
		id, username, hash, role, status string
		createdAt, updatedAt             = time.Time{}, time.Time{}
	)
	if err := s.Scan(&id, &username, &hash, &role, &status, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, user.ErrUserNotFound
		}
		return nil, err
	}
	u := &user.User{}
	u.Rewrite(user.ID(id), username, hash, user.Role(role), user.Status(status), createdAt, updatedAt)
	return u, nil
}

// isDuplicateEntry 判断 MySQL 1062 唯一键冲突。
func isDuplicateEntry(err error) bool {
	var drvErr *mysqldrv.MySQLError
	return errors.As(err, &drvErr) && drvErr.Number == 1062
}
