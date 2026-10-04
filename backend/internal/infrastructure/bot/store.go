package bot

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/codeporter/code-porter/internal/domain/bot"
	"github.com/codeporter/code-porter/internal/domain/model"
	"github.com/codeporter/code-porter/internal/domain/task"
	"github.com/codeporter/code-porter/internal/domain/user"
)

// FileBotRepository 以 JSON 文件持久化机器人配置。
//
// 选文件而非数据库的原因：MVP 阶段机器人数量是十级别，
// 引入数据库会让 docker compose 从「起一个容器」变成「起两个容器 + 卷 + 迁移」。
// 读写全程加锁，落盘用「写临时文件 + rename」保证不会出现半截文件。
type FileBotRepository struct {
	path string
	mu   sync.RWMutex
	m    map[string]*bot.Bot
}

// NewFileBotRepository 构造仓储并加载已有数据；文件不存在时创建空仓储。
func NewFileBotRepository(path string) (*FileBotRepository, error) {
	if path == "" {
		path = "data/bots.json"
	}
	r := &FileBotRepository{path: path, m: make(map[string]*bot.Bot)}
	if err := r.load(); err != nil {
		return nil, err
	}
	return r, nil
}

// botDTO 机器人的持久化结构。
type botDTO struct {
	ID           string    `json:"id"`
	OwnerID      string    `json:"owner_id,omitempty"`
	Name         string    `json:"name"`
	Channel      string    `json:"channel"`
	Enabled      bool      `json:"enabled"`
	Model        string    `json:"model,omitempty"`
	Mode         string    `json:"mode,omitempty"`
	AgentID      string    `json:"agent_id,omitempty"`
	WebhookURL   string    `json:"webhook_url,omitempty"`
	Secret       string    `json:"secret,omitempty"`
	Token        string    `json:"token,omitempty"`
	AESKey       string    `json:"aes_key,omitempty"`
	SystemPrompt string    `json:"system_prompt,omitempty"`
	MentionOnly  bool      `json:"mention_only"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func toDTO(b *bot.Bot) botDTO {
	return botDTO{
		ID:           string(b.ID()),
		OwnerID:      string(b.OwnerID()),
		Name:         b.Name(),
		Channel:      string(b.Channel()),
		Enabled:      b.Enabled(),
		Model:        b.Model().String(),
		Mode:         b.Mode().String(),
		AgentID:      b.AgentID(),
		WebhookURL:   b.WebhookURL(),
		Secret:       b.Secret(),
		Token:        b.Token(),
		AESKey:       b.AESKey(),
		SystemPrompt: b.SystemPrompt(),
		MentionOnly:  b.MentionOnly(),
		CreatedAt:    b.CreatedAt(),
		UpdatedAt:    b.UpdatedAt(),
	}
}

func fromDTO(d botDTO) *bot.Bot {
	if d.Channel == "" {
		d.Channel = string(bot.ChannelFeishu)
	}
	if d.UpdatedAt.IsZero() {
		d.UpdatedAt = time.Now()
	}
	if d.CreatedAt.IsZero() {
		d.CreatedAt = d.UpdatedAt
	}
	// 旧版 bots.json 没有 owner 字段：统一挂到种子 admin 名下，随后由迁移器导入 MySQL。
	ownerID := user.ID(d.OwnerID)
	if ownerID == "" {
		ownerID = user.SeedAdminID
	}
	// 领域对象字段私有：先构造，再回填持久化的 ID 与时间戳。
	b, err := bot.NewBot(bot.Spec{
		OwnerID:      ownerID,
		Name:         d.Name,
		Channel:      bot.Channel(d.Channel),
		Model:        model.Model(d.Model),
		Mode:         task.DeliveryMode(d.Mode),
		AgentID:      d.AgentID,
		WebhookURL:   d.WebhookURL,
		Secret:       d.Secret,
		Token:        d.Token,
		AESKey:       d.AESKey,
		SystemPrompt: d.SystemPrompt,
		MentionOnly:  d.MentionOnly,
		Enabled:      d.Enabled,
		Now:          d.UpdatedAt,
	})
	if err != nil {
		// 脏数据不应拖垮整个网关启动：跳过该条，对外表现为不存在。
		return nil
	}
	b.Rewrite(bot.ID(d.ID), ownerID, d.CreatedAt, d.UpdatedAt)
	return b
}

// Save 保存机器人并落盘。
func (r *FileBotRepository) Save(_ context.Context, b *bot.Bot) error {
	if b == nil {
		return errors.New("bot repo: nil bot")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.m[string(b.ID())] = b.Clone()
	return r.flush()
}

// Find 按 ID 查询。
func (r *FileBotRepository) Find(_ context.Context, id bot.ID) (*bot.Bot, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	b, ok := r.m[string(id)]
	if !ok || b == nil {
		return nil, bot.ErrBotNotFound
	}
	return b.Clone(), nil
}

// FindAll 返回全部机器人。
func (r *FileBotRepository) FindAll(_ context.Context) ([]*bot.Bot, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*bot.Bot, 0, len(r.m))
	for _, b := range r.m {
		out = append(out, b.Clone())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt().Before(out[j].CreatedAt()) })
	return out, nil
}

// FindByOwner 返回某用户的全部机器人。
func (r *FileBotRepository) FindByOwner(_ context.Context, ownerID user.ID) ([]*bot.Bot, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*bot.Bot, 0, len(r.m))
	for _, b := range r.m {
		if b.OwnerID() == ownerID {
			out = append(out, b.Clone())
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt().Before(out[j].CreatedAt()) })
	return out, nil
}

// FindByChannel 按渠道筛选。
func (r *FileBotRepository) FindByChannel(_ context.Context, ch bot.Channel) ([]*bot.Bot, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*bot.Bot, 0, len(r.m))
	for _, b := range r.m {
		if b.Channel() == ch {
			out = append(out, b.Clone())
		}
	}
	return out, nil
}

// Delete 删除机器人并落盘。
func (r *FileBotRepository) Delete(_ context.Context, id bot.ID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.m, string(id))
	return r.flush()
}

// load 从磁盘加载。
func (r *FileBotRepository) load() error {
	raw, err := os.ReadFile(r.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if len(raw) == 0 {
		return nil
	}
	var list []botDTO
	if err := json.Unmarshal(raw, &list); err != nil {
		return err
	}
	for _, d := range list {
		if b := fromDTO(d); b != nil {
			r.m[string(b.ID())] = b
		}
	}
	return nil
}

// flush 原子落盘（写临时文件后 rename）。
func (r *FileBotRepository) flush() error {
	list := make([]botDTO, 0, len(r.m))
	for _, b := range r.m {
		list = append(list, toDTO(b))
	}
	sort.Slice(list, func(i, j int) bool { return list[i].CreatedAt.Before(list[j].CreatedAt) })
	body, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')

	dir := filepath.Dir(r.path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	tmp := r.path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, r.path)
}
