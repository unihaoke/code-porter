package http

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	authsvc "github.com/codeporter/code-porter/internal/application/auth"
	"github.com/codeporter/code-porter/internal/application/gateway"
	"github.com/codeporter/code-porter/internal/application/port"
	"github.com/codeporter/code-porter/internal/domain/model"
	domainuser "github.com/codeporter/code-porter/internal/domain/user"
	"github.com/codeporter/code-porter/internal/infrastructure/broker"
	"github.com/codeporter/code-porter/internal/infrastructure/config"
	"github.com/codeporter/code-porter/internal/infrastructure/logging"
	"github.com/codeporter/code-porter/internal/infrastructure/persistence/memory"
	"github.com/codeporter/code-porter/internal/infrastructure/security"
	"github.com/codeporter/code-porter/internal/infrastructure/transport/ws"
)

type httpHarness struct {
	srv   *Server
	ts    *httptest.Server
	svc   *authsvc.Service
	token string // admin 会话令牌
}

func newHTTPHarness(t *testing.T) *httpHarness {
	t.Helper()
	log := logging.New(io.Discard, logging.LevelError)
	clock := port.RealClock{}
	policy := gateway.TaskPolicy{QueueMaxLen: 8}

	userRepo := memory.NewUserRepository()
	keyRepo := memory.NewAPIKeyRepository()
	sessRepo := memory.NewSessionRepository()
	taskRepo := memory.NewTaskRepository()
	agentRepo := memory.NewAgentRepository()
	queueRepo := memory.NewTaskQueueRepository()
	eventBroker := broker.NewMemoryBroker()
	hub := ws.NewHub(ws.HubConfig{PingInterval: 30 * time.Second}, nil, log)

	registry := gateway.NewAgentRegistry(agentRepo, queueRepo, clock, log, policy)
	submit := gateway.NewSubmitTaskUseCase(taskRepo, queueRepo, registry, eventBroker, hub, clock, log, policy)
	chat := gateway.NewChatUseCase(submit, model.ClaudeCode, log)
	pull := gateway.NewPullTasksUseCase(taskRepo, queueRepo, registry, clock, log, policy)
	ack := gateway.NewAckTaskUseCase(taskRepo, queueRepo, registry, eventBroker, clock, log, policy)
	health := gateway.NewReportHealthUseCase(registry, clock, log, policy)

	svc := authsvc.NewService(authsvc.Deps{
		Users:     userRepo,
		Keys:      keyRepo,
		Sessions:  sessRepo,
		Hasher:    security.NewBcryptHasher(),
		Secrets:   security.NewSHA256Hasher(),
		Generator: security.NewRandomGenerator(),
		Clock:     clock,
		Logger:    log,
	})

	// 内存装配模拟迁移种子 admin（真实环境由 0001_init.sql INSERT IGNORE 完成）。
	adminHash, err := security.NewBcryptHasher().Hash("admin123")
	if err != nil {
		t.Fatal(err)
	}
	seedAdmin, err := domainuser.NewUser(domainuser.Spec{
		ID: domainuser.SeedAdminID, Username: "admin", PasswordHash: adminHash, Role: domainuser.RoleAdmin,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := userRepo.Save(context.Background(), seedAdmin); err != nil {
		t.Fatal(err)
	}

	cfg := &config.GatewayConfig{}
	srv := NewServer(Deps{
		Config: cfg, Auth: svc, Registry: registry,
		Submit: submit, Chat: chat, Pull: pull, Ack: ack, Health: health,
		Hub: hub, QueueRepo: queueRepo, TaskRepo: taskRepo, Users: userRepo,
		Logger: log, Policy: policy,
	})
	ts := httptest.NewServer(srv.httpServer.Handler)
	t.Cleanup(ts.Close)

	h := &httpHarness{srv: srv, ts: ts, svc: svc}
	h.token = h.mustLogin("admin", "admin123")
	return h
}

func (h *httpHarness) mustLogin(username, password string) string {
	token := h.login(username, password, http.StatusOK)
	return token
}

func (h *httpHarness) login(username, password string, wantStatus int) string {
	body, _ := json.Marshal(map[string]string{"username": username, "password": password})
	resp, err := http.Post(h.ts.URL+"/api/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != wantStatus {
		raw, _ := io.ReadAll(resp.Body)
		panic("login status " + resp.Status + ": " + string(raw))
	}
	if wantStatus != http.StatusOK {
		return ""
	}
	var res struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		panic(err)
	}
	return res.Token
}

func (h *httpHarness) req(method, path, token string, body any) (int, map[string]any) {
	var reader io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(method, h.ts.URL+path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

// createKey 通过 API 创建秘钥，返回明文。
// perms 可选：首元素为文件操作权限（read/write/all），缺省由后端补 all。
func (h *httpHarness) createKey(token, name string, scopes []string, t *testing.T, perms ...string) string {
	t.Helper()
	body := map[string]any{"name": name, "scopes": scopes}
	if len(perms) > 0 {
		body["permission"] = perms[0]
	}
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, h.ts.URL+"/api/keys", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		data, _ := io.ReadAll(resp.Body)
		t.Fatalf("create key status=%d body=%s", resp.StatusCode, data)
	}
	var out struct {
		Key struct {
			Secret     string `json:"secret"`
			Permission string `json:"permission"`
		} `json:"key"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Key.Secret == "" {
		t.Fatal("secret missing")
	}
	if len(perms) > 0 && out.Key.Permission != perms[0] {
		t.Fatalf("permission echo = %q, want %q", out.Key.Permission, perms[0])
	}
	return out.Key.Secret
}

func TestAuthLoginSessionAndRBAC(t *testing.T) {
	h := newHTTPHarness(t)

	// 错误密码 / 不存在用户统一 401。
	for _, tc := range [][2]string{{"admin", "wrong"}, {"ghost", "x"}} {
		raw, _ := json.Marshal(map[string]string{"username": tc[0], "password": tc[1]})
		resp, err := http.Post(h.ts.URL+"/api/auth/login", "application/json", bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%v want 401 got %d", tc, resp.StatusCode)
		}
	}

	// 无令牌访问受保护接口 401。
	if status, _ := h.req(http.MethodGet, "/api/auth/me", "", nil); status != http.StatusUnauthorized {
		t.Fatalf("me without token = %d", status)
	}
	// admin /me。
	if status, body := h.req(http.MethodGet, "/api/auth/me", h.token, nil); status != http.StatusOK {
		t.Fatalf("me = %d %v", status, body)
	}

	// admin 创建 member；member 不能列用户（403），admin 可以（200）。
	status, body := h.req(http.MethodPost, "/api/users", h.token,
		map[string]string{"username": "bob", "password": "secret123", "role": "member"})
	if status != http.StatusCreated {
		t.Fatalf("create user = %d %v", status, body)
	}
	bobToken := h.mustLogin("bob", "secret123")
	if status, _ := h.req(http.MethodGet, "/api/users", bobToken, nil); status != http.StatusForbidden {
		t.Fatalf("member list users = %d", status)
	}
	if status, _ := h.req(http.MethodGet, "/api/users", h.token, nil); status != http.StatusOK {
		t.Fatalf("admin list users = %d", status)
	}

	// 登出后令牌失效。
	if status, _ := h.req(http.MethodPost, "/api/auth/logout", bobToken, nil); status != http.StatusOK {
		t.Fatalf("logout = %d", status)
	}
	if status, _ := h.req(http.MethodGet, "/api/auth/me", bobToken, nil); status != http.StatusUnauthorized {
		t.Fatalf("token after logout = %d", status)
	}
}

func TestAPIKeyScopeAndTenancy(t *testing.T) {
	h := newHTTPHarness(t)

	// 创建 member alice（admin API）并登录。
	status, _ := h.req(http.MethodPost, "/api/users", h.token,
		map[string]string{"username": "alice", "password": "secret123", "role": "member"})
	if status != http.StatusCreated {
		t.Fatalf("create alice = %d", status)
	}
	aliceToken := h.mustLogin("alice", "secret123")

	// 无秘钥调 v1 → 401。
	if status := h.v1Post("", nil, nil); status != http.StatusUnauthorized {
		t.Fatalf("v1 without key = %d", status)
	}

	// alice 名下没有实例，双 scope 秘钥调 v1 → 503。
	dual := h.createKey(aliceToken, "dual", nil, t)
	if status := h.v1Post(dual, nil, nil); status != http.StatusServiceUnavailable {
		t.Fatalf("v1 with no agents = %d, want 503", status)
	}

	// 仅 agent scope 秘钥调 v1 → 403。
	agentOnly := h.createKey(aliceToken, "agent-only", []string{"agent"}, t)
	if status := h.v1Post(agentOnly, nil, nil); status != http.StatusForbidden {
		t.Fatalf("agent-only key on v1 = %d, want 403", status)
	}

	// 仅 api scope 秘钥注册实例 → 401/403（401 scope denied 走 forbidden=403）。
	apiOnly := h.createKey(aliceToken, "api-only", []string{"api"}, t)
	if status := h.agentPull(apiOnly, "agt_alice1"); status != http.StatusForbidden {
		t.Fatalf("api-only key pull = %d, want 403", status)
	}

	// agent scope 秘钥注册两台实例成功（200）。
	if status := h.agentPull(agentOnly, "agt_alice1"); status != http.StatusOK {
		t.Fatalf("first pull register = %d", status)
	}
	if status := h.agentPull(agentOnly, "agt_alice2"); status != http.StatusOK {
		t.Fatalf("second pull register = %d", status)
	}

	// 未指定实例调 v1 → 409 且带两台实例清单。
	resp, payload := h.v1PostFull(dual, nil, nil)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("v1 no target with 2 agents = %d, want 409", resp.StatusCode)
	}
	choices, ok := payload["agents"].([]any)
	if !ok || len(choices) != 2 {
		t.Fatalf("409 must list 2 agents, got %v", payload)
	}

	// bob 注册一台实例；alice 指定 bob 的实例 → 404，跨租户隔离。
	h.req(http.MethodPost, "/api/users", h.token,
		map[string]string{"username": "bob", "password": "secret123", "role": "member"})
	bobToken := h.mustLogin("bob", "secret123")
	bobKey := h.createKey(bobToken, "bob-dual", nil, t)
	if status := h.agentPull(bobKey, "agt_bob1"); status != http.StatusOK {
		t.Fatalf("bob register = %d", status)
	}
	if status := h.v1Post(dual, map[string]string{"X-CodePorter-Agent": "agt_bob1"}, nil); status != http.StatusNotFound {
		t.Fatalf("alice targets bob agent = %d, want 404", status)
	}

	// 删除秘钥后立即失效。
	keyID := h.firstKeyID(t, aliceToken)
	if status, _ := h.req(http.MethodDelete, "/api/keys/"+keyID, aliceToken, nil); status != http.StatusOK {
		t.Fatalf("delete key = %d", status)
	}
	if status := h.v1Post(dual, nil, nil); status != http.StatusUnauthorized {
		t.Fatalf("deleted key = %d, want 401", status)
	}
}

func (h *httpHarness) firstKeyID(t *testing.T, token string) string {
	t.Helper()
	status, body := h.req(http.MethodGet, "/api/keys", token, nil)
	if status != http.StatusOK {
		t.Fatalf("list keys = %d", status)
	}
	list, _ := body["keys"].([]any)
	// 列表按创建时间升序，首个即 dual。
	if len(list) == 0 {
		t.Fatal("no keys")
	}
	first, _ := list[0].(map[string]any)
	id, _ := first["id"].(string)
	return id
}

func (h *httpHarness) v1Post(secret string, headers map[string]string, body any) int {
	resp, _ := h.v1PostFull(secret, headers, body)
	return resp.StatusCode
}

func (h *httpHarness) v1PostFull(secret string, headers map[string]string, body any) (*http.Response, map[string]any) {
	if body == nil {
		body = map[string]any{"model": "claude-code", "messages": []map[string]string{{"role": "user", "content": "hi"}}}
	}
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, h.ts.URL+"/v1/chat/completions", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	if secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		panic(err)
	}
	// 不消费流式长连接：非 2xx 时读完 body，2xx 立即关闭（任务侧由 TTL 回收）。
	out := map[string]any{}
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		_ = json.Unmarshal(data, &out)
	}
	resp.Body.Close()
	return resp, out
}

func (h *httpHarness) agentPull(secret, instanceID string) int {
	req, _ := http.NewRequest(http.MethodGet, h.ts.URL+"/agent/pull?maxBatch=1", nil)
	req.Header.Set("X-Agent-Token", secret)
	req.Header.Set("X-Agent-ID", instanceID)
	req.Header.Set("X-Agent-Name", "test-pc")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		panic(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}
