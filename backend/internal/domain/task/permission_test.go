package task

import "testing"

func TestParsePermission(t *testing.T) {
	cases := []struct {
		in   string
		want Permission
		ok   bool
	}{
		{"", PermissionAll, true}, // 缺省 all（兼容无秘钥入口与旧任务）
		{"read", PermissionRead, true},
		{"WRITE", PermissionWrite, true}, // 大小写/空白容错
		{" all ", PermissionAll, true},
		{"root", "", false},
		{"delete", "", false},
	}
	for _, c := range cases {
		got, ok := ParsePermission(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("ParsePermission(%q) = %q,%v want %q,%v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestRestrictPermission(t *testing.T) {
	cases := []struct {
		name     string
		key, req Permission
		want     Permission
	}{
		{"read key cannot escalate via header", PermissionRead, PermissionAll, PermissionRead},
		{"write key cannot escalate to all", PermissionWrite, PermissionAll, PermissionWrite},
		{"all key can be tightened to read", PermissionAll, PermissionRead, PermissionRead},
		{"all key can be tightened to write", PermissionAll, PermissionWrite, PermissionWrite},
		{"request tighter than write key", PermissionWrite, PermissionRead, PermissionRead},
		{"both all", PermissionAll, PermissionAll, PermissionAll},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := RestrictPermission(c.key, c.req); got != c.want {
				t.Fatalf("Restrict(%s,%s) = %s, want %s", c.key, c.req, got, c.want)
			}
		})
	}
}

func TestRequestNormalizeDefaultsToAll(t *testing.T) {
	r := Request{}
	r.Normalize()
	if r.Permission != PermissionAll {
		t.Fatalf("normalized permission = %q, want all", r.Permission)
	}
	r2 := Request{Permission: PermissionRead}
	r2.Normalize()
	if r2.Permission != PermissionRead {
		t.Fatalf("explicit permission must be kept, got %q", r2.Permission)
	}
}
