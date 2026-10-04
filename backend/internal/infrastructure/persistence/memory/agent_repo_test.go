package memory

import (
	"context"
	"testing"
	"time"

	"github.com/codeporter/code-porter/internal/domain/agent"
	"github.com/codeporter/code-porter/internal/domain/user"
)

func TestAgentRepositoryIdentity(t *testing.T) {
	ctx := context.Background()
	r := NewAgentRepository()

	a1, err := agent.Register(agent.Spec{ID: "agt_1", OwnerID: user.SeedAdminID, Name: "pc-a"})
	if err != nil {
		t.Fatalf("register a1: %v", err)
	}
	a2, err := agent.Register(agent.Spec{ID: "agt_2", OwnerID: user.ID("usr_bob"), Name: "pc-b"})
	if err != nil {
		t.Fatalf("register a2: %v", err)
	}
	if err := r.Save(ctx, a1); err != nil {
		t.Fatalf("save a1: %v", err)
	}
	if err := r.Save(ctx, a2); err != nil {
		t.Fatalf("save a2: %v", err)
	}

	// 正确归属可查到。
	got, err := r.FindByIdentity(ctx, user.SeedAdminID, "agt_1")
	if err != nil {
		t.Fatalf("find identity: %v", err)
	}
	if got.Name() != "pc-a" {
		t.Fatalf("unexpected agent: %s", got.Name())
	}

	// 跨属主查实例：不可见（不泄露存在性）。
	if _, err := r.FindByIdentity(ctx, user.ID("usr_bob"), "agt_1"); err != agent.ErrAgentNotFound {
		t.Fatalf("foreign owner must not see instance, got %v", err)
	}

	// 按属主列出。
	listA, err := r.ListByOwner(ctx, user.SeedAdminID)
	if err != nil || len(listA) != 1 || listA[0].ID() != "agt_1" {
		t.Fatalf("ListByOwner admin mismatch: %v %d", err, len(listA))
	}

	all, err := r.List(ctx)
	if err != nil || len(all) != 2 {
		t.Fatalf("List all mismatch: %v %d", err, len(all))
	}

	// 出参是副本：改返回对象不影响仓储内状态。
	a1.MarkOnline(time.Now())
	_ = r.Save(ctx, a1)
	got2, _ := r.Find(ctx, "agt_1")
	got2.MarkOffline()
	got3, _ := r.Find(ctx, "agt_1")
	if got3.Status() != agent.StatusOnline {
		t.Fatalf("copy mutation leaked into repository: %s", got3.Status())
	}
}
