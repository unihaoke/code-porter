package config

// 本文件实现「.env 优先、缺省回退 yaml」的配置覆盖。
//
// 覆盖优先级（从高到低）：
// 1. 进程环境变量（OS env，docker compose / 手动 export）
// 2. .env 文件（从配置文件所在目录逐级向上查找）
// 3. yaml 配置文件
//
// 仅当环境侧取到非空值时才覆盖 yaml；空串 / 纯空白视为未配置，不写回 yaml。
// 旧版本的 ADMIN_TOKEN/API_KEY/AGENT_TOKEN 等环境变量已失效，仅打印告警。

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// deprecatedGatewayEnv 已废弃环境变量（检测到即告警，绝不回退使用）。
var deprecatedGatewayEnv = []string{
	"ADMIN_TOKEN", "API_KEY", "AGENT_TOKEN", "AGENT_TOKENS",
	"GATEWAY_ADMIN_TOKEN", "GATEWAY_AGENT_TOKEN", "GATEWAY_AGENT_ID", "GATEWAY_AGENT_NAME",
}

// DeprecatedEnvWarnings 返回已设置但已废弃的环境变量告警（main 启动时调用）。
// 同时检查 OS env 与配置文件目录向上查找出的 .env。
func DeprecatedEnvWarnings(configPath string) []string {
	d := loadDotEnv(configPath)
	out := make([]string, 0)
	for _, name := range deprecatedGatewayEnv {
		if v := d.lookup(name); v != "" || os.Getenv(name) != "" {
			out = append(out, "环境变量 "+name+" 已废弃且不再生效，请改用 MYSQL_DSN 与控制台账号/秘钥体系")
		}
	}
	return out
}

func applyGatewayEnv(cfg *GatewayConfig, configPath string) {
	d := loadDotEnv(configPath)

	setIfNonEmpty := func(name string, target *string) {
		if v := d.lookup(name); v != "" {
			*target = v
		}
	}
	setIntIfNonEmpty := func(name string, target *int) {
		v := d.lookup(name)
		n, err := strconv.Atoi(v)
		if v != "" && err == nil {
			*target = n
		}
	}

	// 网关（MySQL 与会话）。
	setIfNonEmpty("MYSQL_DSN", &cfg.Database.DSN)
	setIntIfNonEmpty("DB_MAX_OPEN_CONNS", &cfg.Database.MaxOpenConns)
	setIntIfNonEmpty("DB_MAX_IDLE_CONNS", &cfg.Database.MaxIdleConns)
	if v := d.lookup("DB_CONN_MAX_LIFETIME"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.Database.ConnMaxLifetime = d
		}
	}
	if v := d.lookup("AUTH_SESSION_TTL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.Auth.SessionTTL = d
		}
	}

	setIfNonEmpty("GATEWAY_ADDR", &cfg.Server.Addr)

	setIfNonEmpty("CHAT_DEFAULT_MODEL", &cfg.Chat.DefaultModel)
	setIfNonEmpty("LOG_LEVEL", &cfg.Log.Level)
}

// applyAgentEnv 用 .env/OS 环境变量覆盖 Agent 配置。
func applyAgentEnv(cfg *AgentConfig, configPath string) {
	d := loadDotEnv(configPath)

	set := func(name string, target *string) {
		if v := d.lookup(name); v != "" {
			*target = v
		}
	}
	set("AGENT_ID", &cfg.Agent.ID)
	set("AGENT_KEY", &cfg.Agent.Key)
	set("AGENT_NAME", &cfg.Agent.Name)
	set("AGENT_GATEWAY_ADDR", &cfg.Gateway.Addr)
	setBool := func(name string, target *bool) {
		if v := d.lookup(name); v != "" {
			*target = strings.EqualFold(v, "true") || v == "1"
		}
	}
	setInt := func(name string, target *int) {
		if v := d.lookup(name); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				*target = n
			}
		}
	}
	setInt("AGENT_MAX_CONCURRENCY", &cfg.WorkerPool.MaxConcurrency)
	setBool("AGENT_DIRECT_ENABLED", &cfg.Direct.Enabled)
	setBool("AGENT_GATEWAY_INSECURE_TLS", &cfg.Gateway.InsecureTLS)

	// Agent 侧 AI 密钥只允许 .env / OS env 注入，不写回 yaml。
	set("ANTHROPIC_API_KEY", &cfg.Secrets.AnthropicAPIKey)
	set("OPENAI_API_KEY", &cfg.Secrets.OpenAIAPIKey)

	// 本地飞书机器人：凭证建议走环境变量；FEISHU_BOT_ENABLED 可无文件启用。
	set("FEISHU_APP_ID", &cfg.Bots.Feishu.AppID)
	set("FEISHU_APP_SECRET", &cfg.Bots.Feishu.AppSecret)
	set("FEISHU_BOT_MODEL", &cfg.Bots.Feishu.Model)
	setBool("FEISHU_BOT_ENABLED", &cfg.Bots.Feishu.Enabled)
	setBool("FEISHU_BOT_MENTION_ONLY", &cfg.Bots.Feishu.MentionOnly)

	// 本地企业微信机器人（智能机器人 API 模式）。
	set("WECOM_BOT_ID", &cfg.Bots.WeCom.BotID)
	set("WECOM_BOT_SECRET", &cfg.Bots.WeCom.Secret)
	set("WECOM_BOT_MODEL", &cfg.Bots.WeCom.Model)
	setBool("WECOM_BOT_ENABLED", &cfg.Bots.WeCom.Enabled)
	setBool("WECOM_BOT_MENTION_ONLY", &cfg.Bots.WeCom.MentionOnly)

	set("LOG_LEVEL", &cfg.Log.Level)
}
