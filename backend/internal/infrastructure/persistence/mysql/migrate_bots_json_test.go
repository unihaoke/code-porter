package mysql

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/codeporter/code-porter/internal/domain/bot"
	"github.com/codeporter/code-porter/internal/domain/user"
)

// fakeBotRepo 测试迁移器用的最小内存实现（不依赖真实 MySQL）。
type fakeBotRepo struct {
	byID map[bot.ID]*bot.Bot
}

func newFakeBotRepo() *fakeBotRepo { return &fakeBotRepo{byID: map[bot.ID]*bot.Bot{}} }

func (f *fakeBotRepo) FindAll(context.Context) ([]*bot.Bot, error) {
	out := make([]*bot.Bot, 0, len(f.byID))
	for _, b := range f.byID {
		out = append(out, b.Clone())
	}
	return out, nil
}

func (f *fakeBotRepo) Save(_ context.Context, b *bot.Bot) error {
	f.byID[b.ID()] = b.Clone()
	return nil
}

func writeLegacyFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
}

func TestMigrateBotsJSONSuccess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bots.json")
	ts := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	writeLegacyFile(t, path, `[
		{"id":"bot_1","name":"feishu-a","channel":"feishu","enabled":true,"created_at":"`+ts.Format(time.RFC3339)+`","updated_at":"`+ts.Format(time.RFC3339)+`"},
		{"id":"bot_2","name":"wecom-b","channel":"wecom","enabled":false,"agent_id":"agt_x"}
	]`)
	repo := newFakeBotRepo()
	n, err := MigrateBotsJSON(context.Background(), repo, path, user.SeedAdminID)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if n != 2 {
		t.Fatalf("imported = %d, want 2", n)
	}
	b1 := repo.byID["bot_1"]
	if b1 == nil || b1.OwnerID() != user.SeedAdminID || b1.Channel() != bot.ChannelFeishu {
		t.Fatalf("bot_1 restore mismatch: %+v", b1)
	}
	if !b1.CreatedAt().Equal(ts) {
		t.Fatalf("created_at restore mismatch: %s", b1.CreatedAt())
	}
	if b2 := repo.byID["bot_2"]; b2 == nil || b2.AgentID() != "agt_x" {
		t.Fatalf("bot_2 restore mismatch: %+v", b2)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("original file should be renamed, stat err=%v", err)
	}
	if _, err := os.Stat(path + ".migrated"); err != nil {
		t.Fatalf("migrated file should exist: %v", err)
	}
}

func TestMigrateBotsJSONSkipsWhenNonEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bots.json")
	writeLegacyFile(t, path, `[{"id":"bot_1","name":"x","channel":"feishu"}]`)
	repo := newFakeBotRepo()
	existing, _ := bot.NewBot(bot.Spec{OwnerID: user.SeedAdminID, Name: "existing", Channel: bot.ChannelFeishu})
	_ = repo.Save(context.Background(), existing)

	n, err := MigrateBotsJSON(context.Background(), repo, path, user.SeedAdminID)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if n != 0 {
		t.Fatalf("non-empty table must skip, imported=%d", n)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file must be untouched when table non-empty: %v", err)
	}
}

func TestMigrateBotsJSONMissingFile(t *testing.T) {
	repo := newFakeBotRepo()
	n, err := MigrateBotsJSON(context.Background(), repo, filepath.Join(t.TempDir(), "absent.json"), user.SeedAdminID)
	if err != nil || n != 0 {
		t.Fatalf("missing file should be no-op, n=%d err=%v", n, err)
	}
}
