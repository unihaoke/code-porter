package http

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/codeporter/code-porter/internal/infrastructure/transport/ws"
)

// TestAgentWebSocketPersistsAfterUpgrade 回归：/agent/ws 升级成功后，HTTP handler
// 立即返回、r.Context() 被取消，但长连接必须挂在服务级根上下文上继续存活。
// 修复前连接在毫秒级被关闭（Agent 侧重连风暴、网关 ws_conns 恒为 0）。
func TestAgentWebSocketPersistsAfterUpgrade(t *testing.T) {
	h := newHTTPHarness(t)

	status, _ := h.req(http.MethodPost, "/api/users", h.token,
		map[string]string{"username": "carol", "password": "secret123", "role": "member"})
	if status != http.StatusCreated {
		t.Fatalf("create carol = %d", status)
	}
	carolToken := h.mustLogin("carol", "secret123")
	secret := h.createKey(carolToken, "ws-agent", []string{"agent"}, t)

	wsURL := strings.Replace(h.ts.URL, "http://", "ws://", 1) + "/agent/ws?agentId=agt_ws1"
	header := http.Header{}
	header.Set("X-Agent-Token", secret)
	header.Set("X-Agent-ID", "agt_ws1")
	header.Set("X-Agent-Name", "ws-pc")

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		t.Fatalf("dial agent websocket: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	// 走应用层心跳协议（服务端显式回 MsgTypePong），同时验证读循环仍然活着。
	expectPong := func(stage string) {
		t.Helper()
		ping, _ := ws.Encode(ws.Message{Type: ws.MsgTypePing, At: time.Now()})
		if err := conn.WriteMessage(websocket.TextMessage, ping); err != nil {
			t.Fatalf("%s: write ping: %v", stage, err)
		}
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, data, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("%s: connection did not survive: %v", stage, err)
		}
		msg, derr := ws.Decode(data)
		if derr != nil || msg.Type != ws.MsgTypePong {
			t.Fatalf("%s: want pong, got %#v decode=%v", stage, msg, derr)
		}
	}

	// 等过 HTTP handler 返回的时刻：旧实现此刻连接已被 r.Context() 取消。
	time.Sleep(300 * time.Millisecond)
	expectPong("after handler return")
	time.Sleep(300 * time.Millisecond)
	expectPong("after another interval")

	// 网关在线连接数为 1。
	if _, body := h.req(http.MethodGet, "/healthz", "", nil); body["ws_conns"] != float64(1) {
		t.Fatalf("ws_conns = %v, want 1", body["ws_conns"])
	}

	// 实例已登记并标记在线。
	if status, body := h.req(http.MethodGet, "/api/agents", carolToken, nil); status != http.StatusOK {
		t.Fatalf("list agents = %d", status)
	} else {
		list, _ := body["agents"].([]any)
		if len(list) != 1 {
			t.Fatalf("agents = %v", body)
		}
		row, _ := list[0].(map[string]any)
		if row["status"] != "online" {
			t.Fatalf("agent status = %v, want online", row["status"])
		}
	}
}
