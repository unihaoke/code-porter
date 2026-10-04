package http

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

// submitStreamingV1 以后台 goroutine 发起 stream=true 的 v1 调用。
// 返回 ready（响应头已到达、任务已入队后关闭）与取消函数。
func (h *httpHarness) submitStreamingV1(t *testing.T, secret string, headers map[string]string) (<-chan struct{}, context.CancelFunc) {
	t.Helper()
	body := map[string]any{
		"model":    "claude-code",
		"messages": []map[string]string{{"role": "user", "content": "分析这个函数"}},
		"stream":   true,
	}
	raw, _ := json.Marshal(body)
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, h.ts.URL+"/v1/chat/completions", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+secret)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	ready := make(chan struct{})
	go func() {
		defer close(ready)
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
	}()
	t.Cleanup(cancel)
	return ready, cancel
}

// pullDispatch 用秘钥拉取一个任务并返回首个任务的原始 JSON。
func (h *httpHarness) pullDispatch(t *testing.T, secret, instanceID string) map[string]any {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, h.ts.URL+"/agent/pull?maxBatch=1", nil)
	req.Header.Set("X-Agent-Token", secret)
	req.Header.Set("X-Agent-ID", instanceID)
	req.Header.Set("X-Agent-Name", "perm-pc")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		t.Fatalf("pull status=%d body=%s", resp.StatusCode, data)
	}
	var out struct {
		Tasks []map[string]any `json:"tasks"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Tasks) == 0 {
		t.Fatal("pull returned no tasks")
	}
	return out.Tasks[0]
}

// TestAPIKeyPermissionEnforced 验证秘钥文件权限贯穿到下发任务，且请求头无法提权。
func TestAPIKeyPermissionEnforced(t *testing.T) {
	h := newHTTPHarness(t)

	status, _ := h.req(http.MethodPost, "/api/users", h.token,
		map[string]string{"username": "dave", "password": "secret123", "role": "member"})
	if status != http.StatusCreated {
		t.Fatalf("create dave = %d", status)
	}
	daveToken := h.mustLogin("dave", "secret123")

	// 双 scope 的 read 秘钥：既能注册实例，又能发起 v1 调用。
	readKey := h.createKey(daveToken, "readonly", nil, t, "read")
	if status := h.agentPull(readKey, "agt_read1"); status != http.StatusOK {
		t.Fatalf("register read agent = %d", status)
	}

	// 请求头声称 all 试图提权：实际下发必须仍是 read。
	ready1, cancel1 := h.submitStreamingV1(t, readKey, map[string]string{
		"X-CodePorter-Permission": "all",
		"X-CodePorter-Agent":      "agt_read1",
	})
	<-ready1
	d1 := h.pullDispatch(t, readKey, "agt_read1")
	if d1["permission"] != "read" {
		t.Fatalf("read key must not escalate via header, got permission=%v", d1["permission"])
	}
	cancel1()

	// all 秘钥可被请求头收紧为 read。
	allKey := h.createKey(daveToken, "full", nil, t, "all")
	if status := h.agentPull(allKey, "agt_all1"); status != http.StatusOK {
		t.Fatalf("register all agent = %d", status)
	}
	ready2, cancel2 := h.submitStreamingV1(t, allKey, map[string]string{
		"X-CodePorter-Permission": "read",
		"X-CodePorter-Agent":      "agt_all1",
	})
	<-ready2
	d2 := h.pullDispatch(t, allKey, "agt_all1")
	if d2["permission"] != "read" {
		t.Fatalf("all key should be tightenable via header, got permission=%v", d2["permission"])
	}
	cancel2()

	// all 秘钥不带权限头：下发为 all。
	ready3, cancel3 := h.submitStreamingV1(t, allKey,
		map[string]string{"X-CodePorter-Agent": "agt_all1"})
	<-ready3
	d3 := h.pullDispatch(t, allKey, "agt_all1")
	if d3["permission"] != "all" {
		t.Fatalf("default dispatch permission = %v, want all", d3["permission"])
	}
	cancel3()

	// 非法权限头 → 400。
	raw, _ := json.Marshal(map[string]any{
		"model":    "claude-code",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	})
	req, _ := http.NewRequest(http.MethodPost, h.ts.URL+"/v1/chat/completions", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+allKey)
	req.Header.Set("X-CodePorter-Permission", "sudo")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid permission header = %d, want 400", resp.StatusCode)
	}

	// 创建秘钥时非法权限 → 400。
	if status, body := h.req(http.MethodPost, "/api/keys", daveToken,
		map[string]any{"name": "bad", "permission": "root"}); status != http.StatusBadRequest {
		t.Fatalf("create key with bad permission = %d body=%v", status, body)
	}
}
