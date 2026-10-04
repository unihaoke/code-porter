// Package config 负责网关与本地 Agent 的 yaml 配置加载与默认值填充。
package config

import (
	crand "crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/codeporter/code-porter/internal/domain/model"
	"github.com/codeporter/code-porter/internal/infrastructure/mcp"
)

// ErrNotFound 配置文件不存在。
var ErrNotFound = errors.New("config file not found")

// GatewayConfig 网关配置。
type GatewayConfig struct {
	Server    ServerConfig     `yaml:"server"`
	Security  SecurityConfig   `yaml:"security,omitempty"` // 已废弃：仅解析旧字段以打印告警，不再产生鉴权效力
	Database  DatabaseConfig   `yaml:"database"`
	Auth      AuthConfig       `yaml:"auth"`
	Task      TaskConfig       `yaml:"task"`
	RateLimit RateLimitConfig  `yaml:"rate_limit"`
	Lifecycle LifecycleConfig  `yaml:"lifecycle"`
	Agent     GatewayAgentConf `yaml:"agent,omitempty"` // 已废弃：仅解析旧字段以打印告警
	// Web 网页控制台（前端静态资源托管）配置。
	Web WebConfig `yaml:"web"`
	// Bot IM 机器人配置。
	Bot BotConfig `yaml:"bot"`
	Log LogConfig `yaml:"log"`
}

// DatabaseConfig MySQL 配置（users/api_keys/sessions/agents/bots 持久化）。
type DatabaseConfig struct {
	// DSN 形如 user:pass@tcp(host:3306)/codeporter?parseTime=true&charset=utf8mb4
	DSN             string        `yaml:"dsn"`
	MaxOpenConns    int           `yaml:"max_open_conns"`
	MaxIdleConns    int           `yaml:"max_id_conns"`
	ConnMaxLifetime time.Duration `yaml:"conn_max_lifetime"`
}

// AuthConfig 账号体系配置。
type AuthConfig struct {
	// SessionTTL 会话有效期，缺省 168h（7 天）。
	SessionTTL time.Duration `yaml:"session_ttl"`
}

// LegacyWarnings 检测已废弃的旧配置字段，返回应在启动时打印的告警。
func (c *GatewayConfig) LegacyWarnings() []string {
	warnings := make([]string, 0, 4)
	if len(c.Security.APIKeys) > 0 {
		warnings = append(warnings, "security.api_keys 已废弃且不再生效：请在控制台为用户创建秘钥")
	}
	if len(c.Security.AgentTokens) > 0 {
		warnings = append(warnings, "security.agent_tokens 已废弃且不再生效：LocalAgent 改用控制台秘钥（agent scope）连接")
	}
	if c.Security.AdminToken != "" {
		warnings = append(warnings, "security.admin_token 已废弃且不再生效：网页端改用账号密码登录 /api/auth/login")
	}
	if c.Agent.ID != "" || c.Agent.Name != "" || c.Agent.Token != "" {
		warnings = append(warnings, "agent 引导块（id/name/token）已废弃且不再生效：实例由客户端持秘钥自注册")
	}
	return warnings
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
// 加载后应用 .env 覆盖（OS env > .env 文件 > yaml，仅非空时覆盖，不写回文件）。
func LoadGateway(path string) (*GatewayConfig, error) {
	cfg := defaultGatewayConfig()
	if path != "" {
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
	}
	applyGatewayEnv(cfg, path)
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
		Database: DatabaseConfig{
			MaxOpenConns:    20,
			MaxIdleConns:    5,
			ConnMaxLifetime: 30 * time.Minute,
		},
		Auth: AuthConfig{SessionTTL: 168 * time.Hour},
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
		// Agent 静态引导已移除：实例由客户端持秘钥自注册。
		Web: WebConfig{Enabled: true, StaticDir: "web/dist"},
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
	// Secrets AI 密钥等敏感配置，写入 yaml 后以环境变量注入 MCP 子进程。
	Secrets SecretsConfig `yaml:"secrets"`
	Log     LogConfig     `yaml:"log"`
}

// SecretsConfig AI 密钥等敏感配置。
type SecretsConfig struct {
	// AnthropicAPIKey Anthropic / Claude 系列密钥（注入 ANTHROPIC_API_KEY）。
	AnthropicAPIKey string `yaml:"anthropic_api_key"`
	// OpenAIAPIKey OpenAI 系列密钥（注入 OPENAI_API_KEY）。
	OpenAIAPIKey string `yaml:"openai_api_key"`
}

// AgentIdentity 本机 Agent 的身份信息。
type AgentIdentity struct {
	// ID 实例 ID，需与秘钥属主名下注册一致；留空时首次启动自动生成 agt_<16hex> 并写回配置。
	ID string `yaml:"id"`
	// Key 控制台生成的连接秘钥（cp_ 开头，含 agent scope）。仅通过 key/env 注入。
	Key string `yaml:"key"`
	// Name 机器名（控制台展示用）；留空首次启动取主机名并写回。
	Name string `yaml:"name"`
	// DeprecatedToken 旧版 token 字段；仅用于检测旧配置并给出明确迁移报错，绝不参与鉴权。
	DeprecatedToken string `yaml:"token,omitempty"`
}

// NewAgentIdentity 旧语义已移除：实例身份不再静态引导，保留构造仅为兼容潜在调用。
// 新代码通过配置加载 + EnsureAgentIdentity 自动补齐。

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
// 加载后应用 .env 覆盖（OS env > .env 文件 > yaml，仅非空时覆盖，不写回文件）。
func LoadAgent(path string) (*AgentConfig, error) {
	cfg := defaultAgentConfig()
	if path != "" {
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
	}
	applyAgentEnv(cfg, path)
	return cfg, nil
}

// SaveAgent 将 Agent 配置写回 yaml 文件。文件权限设为 0600，避免密钥被其他用户读取。
func SaveAgent(path string, cfg *AgentConfig) error {
	raw, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal agent config: %w", err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return fmt.Errorf("write agent config %s: %w", path, err)
	}
	return nil
}

// ErrAgentKeyMissing 未配置连接秘钥。
var ErrAgentKeyMissing = errors.New("agent.key 未配置")

func defaultAgentConfig() *AgentConfig {
	return &AgentConfig{
		Agent:   AgentIdentity{ID: ""},
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
		Secrets: SecretsConfig{},
		Log:     LogConfig{Level: "info"},
	}
}

// EnsureAgentIdentity 校验连接秘钥、自动生成实例 ID/机器名并写回配置文件。
//
// 返回实例 ID。约定：
//   - agent.key 缺失或仍使用旧 token 字段 → 明确报错（旧 token 不再被接受）；
//   - agent.id 留空 → 生成 agt_<16hex>（crypto/rand），与该机器配置 1:1 绑定并写回；
//   - agent.name 留空 → 取主机名并写回。
func EnsureAgentIdentity(cfg *AgentConfig, configPath string) (string, error) {
	// 旧版 AGENT_TOKEN（OS env 或 .env 文件）一律拒绝并给出迁移指引。
	if v := loadDotEnv(configPath).lookup("AGENT_TOKEN"); v != "" {
		return "", errors.New("检测到旧环境变量 AGENT_TOKEN：已废弃，请改用控制台生成的秘钥并设置 AGENT_KEY")
	}
	if strings.TrimSpace(cfg.Agent.DeprecatedToken) != "" {
		return "", errors.New("检测到旧配置 agent.token：已废弃，请改用在控制台「秘钥」页生成的秘钥填入 agent.key（环境变量 AGENT_KEY）")
	}
	if strings.TrimSpace(cfg.Agent.Key) == "" {
		return "", fmt.Errorf("%w：请在控制台「秘钥」页创建含 agent 权限的秘钥，填入配置 agent.key 或环境变量 AGENT_KEY", ErrAgentKeyMissing)
	}

	changed := false
	if cfg.Agent.ID == "" {
		id, err := newInstanceID()
		if err != nil {
			return "", err
		}
		cfg.Agent.ID = id
		changed = true
	}
	if strings.TrimSpace(cfg.Agent.Name) == "" {
		name, err := os.Hostname()
		if err == nil && name != "" {
			cfg.Agent.Name = name
			changed = true
		}
	}
	if changed && configPath != "" {
		if err := SaveAgent(configPath, cfg); err != nil {
			return "", fmt.Errorf("write back agent identity: %w", err)
		}
	}
	return cfg.Agent.ID, nil
}

// newInstanceID 生成 agt_ 前缀的 16 字节随机十六进制实例 ID。
func newInstanceID() (string, error) {
	buf := make([]byte, 16)
	if _, err := crand.Read(buf); err != nil {
		return "", err
	}
	return "agt_" + hex.EncodeToString(buf), nil
}
