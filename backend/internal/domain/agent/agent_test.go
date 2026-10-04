package agent

import (
	"testing"
	"time"

	"github.com/codeporter/code-porter/internal/domain/user"
)

func TestRegisterValidation(t *testing.T) {
	now := time.Date(2026, 10, 4, 10, 0, 0, 0, time.UTC)
	if _, err := Register(Spec{ID: "agt_1", Now: now}); err != ErrOwnerRequired {
		t.Fatalf("missing owner should fail, got %v", err)
	}
	if _, err := Register(Spec{OwnerID: user.ID("usr_a"), Now: now}); err == nil {
		t.Fatalf("missing instance id should fail")
	}
	a, err := Register(Spec{ID: "agt_1", OwnerID: user.ID("usr_a"), Name: "pc-a", Now: now})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if a.OwnerID() != "usr_a" || a.ID() != "agt_1" || a.Name() != "pc-a" {
		t.Fatalf("fields mismatch: %+v", a)
	}
	if a.Status() != StatusOffline {
		t.Fatalf("new instance should be offline")
	}
}

func TestVerifyOwnerAndRename(t *testing.T) {
	now := time.Now()
	a, _ := Register(Spec{ID: "agt_1", OwnerID: user.ID("usr_a"), Name: "pc-a", Now: now})

	if err := a.VerifyOwner("usr_a"); err != nil {
		t.Fatalf("same owner should pass: %v", err)
	}
	if err := a.VerifyOwner("usr_b"); err != ErrOwnerMismatch {
		t.Fatalf("foreign owner should be rejected, got %v", err)
	}
	if err := a.VerifyOwner(""); err != ErrOwnerMismatch {
		t.Fatalf("empty owner should be rejected, got %v", err)
	}

	a.UpdateName("  pc-a-renamed  ")
	if a.Name() != "pc-a-renamed" {
		t.Fatalf("name not updated: %q", a.Name())
	}
	a.UpdateName("   ")
	if a.Name() != "pc-a-renamed" {
		t.Fatalf("blank rename must keep old name")
	}
}

func TestAgentCloneIsolation(t *testing.T) {
	a, _ := Register(Spec{ID: "agt_1", OwnerID: "usr_a", Name: "pc"})
	cp := a.Clone()
	cp.MarkOffline()
	if a.Status() != StatusOffline {
		// 新建即 offline，改为先上线再验证
	}
	a.MarkOnline(time.Now())
	cp2 := a.Clone()
	cp2.MarkOffline()
	if a.Status() != StatusOnline {
		t.Fatalf("clone mutation leaked to original")
	}
}
