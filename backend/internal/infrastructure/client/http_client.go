// Package client 实现 LocalAgent → Gateway 的出站客户端（HTTP Pull/ACK + WebSocket 直连）。
//
// 所有连接均由本地主动发起，本机不监听任何端口。
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/codeporter/code-porter/internal/application/port"
	"github.com/codeporter/code-porter/internal/domain/agent"
	"github.com/codeporter/code-porter/pkg/apperr"
)

// Config 网关客户端配置。
type Config struct {
	// BaseURL 网关地址，如 https://gw.example.com。
	BaseURL string
	// Key 控制台签发的连接秘钥（cp_ 前缀，含 agent scope）。
	Key string
	// InstanceID 本机实例 ID（agt_ 前缀）。
	InstanceID string
	// Name 机器名（首次注册使用）。
	Name string
	// Timeout 单次 HTTP 请求超时。
	Timeout time.Duration
	// InsecureTLS 是否跳过证书校验（自签证书场景）。
	InsecureTLS bool
}

func (c Config) withDefaults() Config {
	if c.Timeout <= 0 {
		c.Timeout = 30 * time.Second
	}
	return c
}

// GatewayClient port.GatewayClient 的 HTTP 实现。
type GatewayClient struct {
	cfg  Config
	http *http.Client
}

// NewGatewayClient 构造客户端。
func NewGatewayClient(cfg Config) *GatewayClient {
	cfg = cfg.withDefaults()
	return &GatewayClient{
		cfg: cfg,
		http: &http.Client{
			Timeout:   cfg.Timeout,
			Transport: newTransport(cfg.InsecureTLS),
		},
	}
}

// setAgentHeaders 为 /agent/* 请求注入鉴权秘钥与实例身份。
func (c *GatewayClient) setAgentHeaders(req *http.Request) {
	if c.cfg.Key != "" {
		req.Header.Set("X-Agent-Token", c.cfg.Key)
	}
	if c.cfg.InstanceID != "" {
		req.Header.Set("X-Agent-ID", c.cfg.InstanceID)
	}
	if c.cfg.Name != "" {
		req.Header.Set("X-Agent-Name", c.cfg.Name)
	}
}

// Pull 拉取待处理任务。
func (c *GatewayClient) Pull(ctx context.Context, agentID string, maxBatch int) ([]port.TaskDispatch, error) {
	url := fmt.Sprintf("%s/agent/pull?agentId=%s&maxBatch=%d", trimSlash(c.cfg.BaseURL), agentID, maxBatch)
	var out pullResponse
	if err := c.doJSON(ctx, http.MethodGet, url, nil, &out); err != nil {
		return nil, err
	}
	return out.Tasks, nil
}

// Ack 上报任务结果与片段。
func (c *GatewayClient) Ack(ctx context.Context, req port.AckRequest) error {
	url := trimSlash(c.cfg.BaseURL) + "/agent/ack"
	var out ackResponse
	return c.doJSON(ctx, http.MethodPost, url, req, &out)
}

// ReportHealth 上报本机健康状态。
func (c *GatewayClient) ReportHealth(ctx context.Context, agentID string, h agent.Health) error {
	url := trimSlash(c.cfg.BaseURL) + "/agent/health"
	body := healthRequest{AgentID: agentID, Health: h}
	var out ackResponse
	return c.doJSON(ctx, http.MethodPost, url, body, &out)
}

type pullResponse struct {
	Tasks     []port.TaskDispatch `json:"tasks"`
	QueueLen  int                 `json:"queue_len"`
	Timestamp int64               `json:"timestamp"`
}

type ackResponse struct {
	OK     bool   `json:"ok"`
	Status string `json:"status,omitempty"`
}

type healthRequest struct {
	AgentID string       `json:"agent_id"`
	Health  agent.Health `json:"health"`
}

func (c *GatewayClient) doJSON(ctx context.Context, method, url string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return apperr.Wrap(apperr.CodeInternal, "marshal request failed", err)
		}
		reader = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return apperr.Wrap(apperr.CodeInternal, "build request failed", err)
	}
	req.Header.Set("Accept", "application/json")
	if strings.Contains(url, "/agent/") {
		req.Header.Set("Content-Type", "application/json")
	}
	c.setAgentHeaders(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return apperr.Wrap(apperr.CodeUnavailable, "call gateway failed", err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return apperr.Wrap(apperr.CodeUnavailable, "read gateway response failed", err)
	}
	if resp.StatusCode >= 300 {
		return apperr.New(mapStatusCode(resp.StatusCode),
			fmt.Sprintf("gateway returned %d: %s", resp.StatusCode, string(raw)))
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return apperr.Wrap(apperr.CodeInternal, "decode gateway response failed", err)
		}
	}
	return nil
}

func mapStatusCode(code int) apperr.Code {
	switch code {
	case http.StatusUnauthorized, http.StatusForbidden:
		return apperr.CodeUnauthorized
	case http.StatusTooManyRequests:
		return apperr.CodeRateLimited
	case http.StatusNotFound:
		return apperr.CodeNotFound
	case http.StatusGatewayTimeout, http.StatusRequestTimeout:
		return apperr.CodeTimeout
	}
	return apperr.CodeUnavailable
}

func trimSlash(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}
