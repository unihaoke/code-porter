package bot

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	domainbot "github.com/codeporter/code-porter/internal/domain/bot"
	"github.com/codeporter/code-porter/internal/domain/user"
)

// TestFileBotRepositoryOwnerRoundtrip 验证 owner 字段写盘/加载往返。
func TestFileBotRepositoryOwnerRoundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bots.json")
	r1, err := NewFileBotRepository(path)
	if err != nil {
		t.Fatalf("new repo: %v", err)
	}
	b, err := domainbot.NewBot(domainbot.Spec{
		OwnerID: user.ID("usr_bob"),
		Name:    "bob-feishu",
		Channel: domainbot.ChannelFeishu,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("new bot: %v", err)
	}
	if err := r1.Save(context.Background(), b); err != nil {
		t.Fatalf("save: %v", err)
	}

	r2, err := NewFileBotRepository(path)
	if err != nil {
		t.Fatalf("reload repo: %v", err)
	}
	got, err := r2.Find(context.Background(), b.ID())
	if err != nil {
		t.Fatalf("find after reload: %v", err)
	}
	if got.OwnerID() != user.ID("usr_bob") {
		t.Fatalf("owner roundtrip mismatch: %s", got.OwnerID())
	}
	list, err := r2.FindByOwner(context.Background(), user.ID("usr_bob"))
	if err != nil || len(list) != 1 {
		t.Fatalf("FindByOwner mismatch: %v %d", err, len(list))
	}
}

// TestFileBotRepositoryLegacyOwnerDefaultsToSeedAdmin 旧版 bots.json 无 owner 字段时挂到种子 admin。
func TestFileBotRepositoryLegacyOwnerDefaultsToSeedAdmin(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bots.json")
	legacy := `[
		{
			"id": "bot_old1",
			"name": "old-bot",
			"channel": "feishu",
			"enabled": true,
			"created_at": "2026-09-01T00:00:00Z",
			"updated_at": "2026-09-01T00:00:00Z"
		}
	]`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatalf("write legacy file: %v", err)
	}
	r, err := NewFileBotRepository(path)
	if err != nil {
		t.Fatalf("load legacy: %v", err)
	}
	got, err := r.Find(context.Background(), domainbot.ID("bot_old1"))
	if err != nil {
		t.Fatalf("find legacy bot: %v", err)
	}
	if got.OwnerID() != user.SeedAdminID {
		t.Fatalf("legacy bot owner should default to seed admin, got %s", got.OwnerID())
	}
}
