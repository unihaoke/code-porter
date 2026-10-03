// Package config 负责网关与本地 Agent 的 yaml 配置加载与默认值填充。
package config

import (
	"errors"
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/codeporter/code-porter/internal/domain/agent"
	"github.com/codeporter/code-porter/internal/domain/model"
	"github.com/codeporter/code-porter/internal/infrastructure/mcp"
)

// ErrNotFound 配置文件不存在。
var ErrNotFound = errors.New("config file not found")

// GatewayConfig 网关配置。
type GatewayConfig struct {
	Server    ServerConfig     `yaml:"server"`
	Security  SecurityConfig   `yaml:"security"`
	Task      TaskConfig       `yaml:"task"`
	RateLimit RateLimitConfig  `yaml:"rate_limit"`
	Lifecycle LifecycleConfig  `yaml:"lifecycle"`
	Agent     GatewayAgentConf `yaml:"agent"`
	// Web 网页控制台（前端静态资源托管）配置。
	Web WebConfig `yaml:"web"`
	// Bot IM 机器人配置。
	Bot BotConfig `yaml:"bot"`
	Log LogConfig `yaml:"log"`
}

// ServerConfig HTTP 服务配置。
type ServerConfig struct {
	// Addr 监听地址，如 :9022。
	Addr string `yaml:"addr"`
	// ReadTimeout 读超时；SSE 长连接需要较大的写超时。
	ReadTimeout time.Duration `yaml:"read_timeout"`
	// WriteTimeout 写超时。
	WriteTimeout time.Duration `yaml:"write_timeout"`
	// IdleTimeout 空闲连接超时。
	IdleTimeout time.Duration `yaml:"idle_timeout"`
	// ShutdownTimeout 优雅退出超时。
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout"`
	// PublicAddr 网关对外可访问地址，如 https://cp.example.com。
	// 仅用于生成机器人回调地址提示，不影响监听行为。
	PublicAddr string `yaml:"public_addr"`
}

// SecurityConfig 安全配置。
type SecurityConfig struct {
	// APIKeys 对外 API Key 列表（MVP 静态配置）。
	APIKeys []string `yaml:"api_keys"`
	// AgentTokens Agent 鉴权令牌列表。
	AgentTokens []string `yaml:"agent_tokens"`
	// AdminToken 网页管理端令牌；为空时回落到第一个 API Key。
	AdminToken string `yaml:"admin_token"`
}

// WebConfig 网页控制台配置。
type WebConfig struct {
	// Enabled 是否托管前端构建产物。
	Enabled bool `yaml:"enabled"`
	// StaticDir 前端 dist 目录路径。
	StaticDir string `yaml:"static_dir"`
}

// BotConfig IM 机器人配置。
type BotConfig struct {
	// StoreFile 机器人配置持久化文件（JSON）。
	StoreFile string `yaml:"store_file"`
	// DefaultModel 机器人未指定模型时使用的本地 AI 工具。
	DefaultModel string `yaml:"default_model"`
	// ReplyTimeout 等待本地 AI 结果的最长时间。
	ReplyTimeout time.Duration `yaml:"reply_timeout"`
}

// TaskConfig 任务策略配置。
type TaskConfig struct {
	MaxRetry          int           `yaml:"max_retry"`
	LockTimeout       time.Duration `yaml:"lock_timeout"`
	TTL               time.Duration `yaml:"ttl"`
	QueueMaxLen       int           `yaml:"queue_max_len"`
	RequestTimeout    time.Duration `yaml:"request_timeout"`
	StreamIdleTimeout time.Duration `yaml:"stream_idle_timeout"`
	MCPTimeout        time.Duration `yaml:"mcp_timeout"`
	// LogPrompt 是否记录完整 prompt（默认关闭，避免敏感代码外泄）。
	LogPrompt bool `yaml:"log_prompt"`
}

// RateLimitConfig 限流配置。
type RateLimitConfig struct {
	// QPS 单 API Key 每秒请求数，0 表示不限。
	QPS float64 `yaml:"qps"`
	// Burst 令牌桶容量。
	Burst int `yaml:"burst"`
	// MaxInflight 全局最大在途请求数，0 表示不限。
	MaxInflight int `yaml:"max_inflight"`
}

// LifecycleConfig 生命周期守护配置。
type LifecycleConfig struct {
	LockSweepInterval      time.Duration `yaml:"lock_sweep_interval"`
	TTLSweepInterval       time.Duration `yaml:"ttl_sweep_interval"`
	HeartbeatSweepInterval time.Duration `yaml:"heartbeat_sweep_interval"`
	RetentionInterval      time.Duration `yaml:"retention_interval"`
	TaskRetention          time.Duration `yaml:"task_retention"`
	// AgentHeartbeatTimeout Agent 心跳超时。
	AgentHeartbeatTimeout time.Duration `yaml:"agent_heartbeat_timeout"`
}

// GatewayAgentConf 网关侧默认 Agent 配置（MVP 单 Agent）。
type GatewayAgentConf struct {
	// ID 默认 Agent ID。
	ID string `yaml:"id"`
	// Name 名称。
	Name string `yaml:"name"`
	// Token 该 Agent 的鉴权令牌。
	Token string `yaml:"token"`
}

// LogConfig 日志配置。
type LogConfig struct {
	// Level debug / info / warn / error。
	Level string `yaml:"level"`
}

// LoadGateway 加载网关配置；path 为空时返回默认配置。
func LoadGateway(path string) (*GatewayConfig, error) {
	cfg := defaultGatewayConfig()
	if path == "" {
		return cfg, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, path)
		}
		return nil, err
	}
	if err := yaml.Unmarshal(raw, cfg); err != nil {
		return nil, fmt.Errorf("parse gateway config %s: %w", path, err)
	}
	return cfg, nil
}

func defaultGatewayConfig() *GatewayConfig {
	return &GatewayConfig{
		Server: ServerConfig{
			Addr:            ":9022",
			ReadTimeout:     30 * time.Second,
			WriteTimeout:    10 * time.Minute, // SSE 长连接
			IdleTimeout:     2 * time.Minute,
			ShutdownTimeout: 20 * time.Second,
		},
		Security: SecurityConfig{},
		Task: TaskConfig{
			MaxRetry:          2,
			LockTimeout:       60 * time.Second,
			TTL:               10 * time.Minute,
			QueueMaxLen:       512,
			RequestTimeout:    5 * time.Minute,
			StreamIdleTimeout: 2 * time.Minute,
			MCPTimeout:        3 * time.Minute,
		},
		RateLimit: RateLimitConfig{QPS: 10, Burst: 20, MaxInflight: 512},
		Lifecycle: LifecycleConfig{
			LockSweepInterval:      5 * time.Second,
			TTLSweepInterval:       30 * time.Second,
			HeartbeatSweepInterval: 10 * time.Second,
			RetentionInterval:      5 * time.Minute,
			TaskRetention:          30 * time.Minute,
			AgentHeartbeatTimeout:  30 * time.Second,
		},
		Agent: GatewayAgentConf{ID: "local-pc", Name: "My PC", Token: "change-me-agent-token"},
		Web:   WebConfig{Enabled: true, StaticDir: "web/dist"},
		Bot: BotConfig{
			StoreFile:    "data/bots.json",
			DefaultModel: string(model.ClaudeCode),
			ReplyTimeout: 10 * time.Minute,
		},
		Log: LogConfig{Level: "info"},
	}
}

// AgentConfig 本地 Agent 配置。
type AgentConfig struct {
	Agent      AgentIdentity    `yaml:"agent"`
	Gateway    GatewayEndpoint  `yaml:"gateway"`
	Pull       PullConfig       `yaml:"pull"`
	WorkerPool WorkerPoolConfig `yaml:"worker_pool"`
	Direct     DirectConfig     `yaml:"direct"`
	Health     HealthConfig     `yaml:"health"`
	MCP        mcp.Config       `yaml:"mcp"`
	Log        LogConfig        `yaml:"log"`
}

// AgentIdentity 本机 Agent 身份。
type AgentIdentity struct {
	// ID Agent ID，需与网关侧一致。
	ID string `yaml:"id"`
	// Token Agent 鉴权令牌。
	Token string `yaml:"token"`
}

// GatewayEndpoint 网关地址与连通参数。
type GatewayEndpoint struct {
	// Addr 网关地址，如 https://gw.example.com。
	Addr string `yaml:"addr"`
	// Timeout HTTP 请求超时。
	Timeout time.Duration `yaml:"timeout"`
	// InsecureTLS 跳过证书校验（自签证书场景）。
	InsecureTLS bool `yaml:"insecure_tls"`
}

// PullConfig Pull 轮询配置。
type PullConfig struct {
	// IntervalMin 有任务时的最快轮询间隔。
	IntervalMin time.Duration `yaml:"interval_min"`
	// IntervalMax 空闲时的最大轮询间隔。
	IntervalMax time.Duration `yaml:"interval_max"`
	// BackoffFactor 退避系数。
	BackoffFactor float64 `yaml:"backoff_factor"`
}

// WorkerPoolConfig 本地协程池配置。
type WorkerPoolConfig struct {
	// MaxConcurrency 最大并发 AI 任务数（默认 2，保护 IDE）。
	MaxConcurrency int `yaml:"max_concurrency"`
	// QueueSize 本地等待队列长度。
	QueueSize int `yaml:"queue_size"`
}

// DirectConfig SSE 直连模式配置。
type DirectConfig struct {
	// Enabled 是否启用直连模式。
	Enabled bool `yaml:"enabled"`
	// HeartbeatInterval 心跳间隔。
	HeartbeatInterval time.Duration `yaml:"heartbeat_interval"`
	// PongTimeout 心跳超时。
	PongTimeout time.Duration `yaml:"pong_timeout"`
	// ReconnectMin / ReconnectMax 重连退避区间。
	ReconnectMin time.Duration `yaml:"reconnect_min"`
	ReconnectMax time.Duration `yaml:"reconnect_max"`
}

// HealthConfig 健康上报配置。
type HealthConfig struct {
	// Interval 上报间隔。
	Interval time.Duration `yaml:"interval"`
}

// LoadAgent 加载 Agent 配置；path 为空时返回默认配置。
func LoadAgent(path string) (*AgentConfig, error) {
	cfg := defaultAgentConfig()
	if path == "" {
		return cfg, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, path)
		}
		return nil, err
	}
	if err := yaml.Unmarshal(raw, cfg); err != nil {
		return nil, fmt.Errorf("parse agent config %s: %w", path, err)
	}
	return cfg, nil
}

func defaultAgentConfig() *AgentConfig {
	return &AgentConfig{
		Agent:   AgentIdentity{ID: "local-pc", Token: "change-me-agent-token"},
		Gateway: GatewayEndpoint{Addr: "http://127.0.0.1:9022", Timeout: 30 * time.Second},
		Pull: PullConfig{
			IntervalMin:   500 * time.Millisecond,
			IntervalMax:   3 * time.Second,
			BackoffFactor: 1.6,
		},
		WorkerPool: WorkerPoolConfig{MaxConcurrency: 2, QueueSize: 4},
		Direct: DirectConfig{
			Enabled:           true,
			HeartbeatInterval: 20 * time.Second,
			PongTimeout:       60 * time.Second,
			ReconnectMin:      time.Second,
			ReconnectMax:      30 * time.Second,
		},
		Health: HealthConfig{Interval: 15 * time.Second},
		MCP: mcp.Config{
			Trae:       mcp.AdapterConfig{Enabled: true, Command: "trae-mcp"},
			ClaudeCode: mcp.AdapterConfig{Enabled: true, Command: "claude"},
			CodeBuddy:  mcp.AdapterConfig{Enabled: true, Command: "codebuddy-mcp"},
			Codex:      mcp.AdapterConfig{Enabled: false, Command: "codex"},
		},
		Log: LogConfig{Level: "info"},
	}
}

// EnsureAgentRegistry 保证配置中的 Agent Token 不为空，返回用于注册的默认值。
func EnsureAgentRegistry(cfg *AgentConfig) agent.ID {
	if cfg.Agent.ID == "" {
		cfg.Agent.ID = "local-pc"
	}
	return agent.ID(cfg.Agent.ID)
}
