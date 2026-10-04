package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFile 写文件并建目录。
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestAgentEnvOverridesYAML(t *testing.T) {
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "configs", "agent.yaml")
	writeFile(t, yamlPath, `
agent:
  id: yaml-id
  key: yaml-key
  name: yaml-host
gateway:
  addr: http://yaml:9022
secrets:
  anthropic_api_key: yaml-anthropic
`)
	writeFile(t, filepath.Join(dir, "configs", ".env"), `
AGENT_ID=env-id
AGENT_KEY=cp_envkey
AGENT_NAME=env-host
AGENT_GATEWAY_ADDR=http://env:9022
ANTHROPIC_API_KEY=env-anthropic
`)

	cfg, err := LoadAgent(yamlPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Agent.ID != "env-id" {
		t.Errorf("Agent.ID = %q, want env-id", cfg.Agent.ID)
	}
	if cfg.Agent.Key != "cp_envkey" {
		t.Errorf("Agent.Key = %q, want cp_envkey", cfg.Agent.Key)
	}
	if cfg.Agent.Name != "env-host" {
		t.Errorf("Agent.Name = %q, want env-host", cfg.Agent.Name)
	}
	if cfg.Gateway.Addr != "http://env:9022" {
		t.Errorf("Gateway.Addr = %q, want env", cfg.Gateway.Addr)
	}
	if cfg.Secrets.AnthropicAPIKey != "env-anthropic" {
		t.Errorf("Secrets.AnthropicAPIKey = %q, want env", cfg.Secrets.AnthropicAPIKey)
	}
}

func TestAgentYAMLWhenNoEnv(t *testing.T) {
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "agent.yaml")
	writeFile(t, yamlPath, `
agent:
  id: yaml-id
  key: cp_yamlkey
gateway:
  addr: http://yaml:9022
`)
	for _, k := range []string{"AGENT_ID", "AGENT_KEY", "AGENT_NAME", "AGENT_GATEWAY_ADDR"} {
		t.Setenv(k, "")
	}
	cfg, err := LoadAgent(yamlPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Agent.ID != "yaml-id" || cfg.Agent.Key != "cp_yamlkey" {
		t.Errorf("no .env: got id=%q key=%q, want yaml values", cfg.Agent.ID, cfg.Agent.Key)
	}
	if cfg.Gateway.Addr != "http://yaml:9022" {
		t.Errorf("no .env: Gateway.Addr = %q, want yaml", cfg.Gateway.Addr)
	}
}

func TestOSEnvBeatsDotEnvFile(t *testing.T) {
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "agent.yaml")
	writeFile(t, yamlPath, "agent:\n  key: yaml-key\n")
	writeFile(t, filepath.Join(dir, ".env"), "AGENT_KEY=from-dotenv\n")
	t.Setenv("AGENT_KEY", "cp_fromos")

	cfg, err := LoadAgent(yamlPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Agent.Key != "cp_fromos" {
		t.Errorf("Agent.Key = %q, want cp_fromos (OS env 优先级最高)", cfg.Agent.Key)
	}
}

func TestEnsureAgentIdentity(t *testing.T) {
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "agent.yaml")
	writeFile(t, yamlPath, "agent:\n  key: cp_testkey\n")
	for _, k := range []string{"AGENT_KEY", "AGENT_TOKEN", "AGENT_ID", "AGENT_NAME"} {
		t.Setenv(k, "")
	}

	// 缺秘钥报错。
	empty, err := LoadAgent(yamlPath)
	if err != nil {
		t.Fatal(err)
	}
	empty.Agent.Key = ""
	if _, err := EnsureAgentIdentity(empty, yamlPath); err == nil {
		t.Fatal("missing key must fail")
	}

	// 旧 token 字段报错。
	legacyPath := filepath.Join(dir, "legacy.yaml")
	writeFile(t, legacyPath, "agent:\n  key: cp_testkey\n  token: old-token\n")
	legacy, err := LoadAgent(legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureAgentIdentity(legacy, legacyPath); err == nil ||
		!strings.Contains(err.Error(), "agent.token") {
		t.Fatalf("legacy yaml token should be rejected, got %v", err)
	}

	// 旧 AGENT_TOKEN 环境变量报错。
	t.Setenv("AGENT_TOKEN", "old-env-token")
	cfg, err := LoadAgent(yamlPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureAgentIdentity(cfg, yamlPath); err == nil ||
		!strings.Contains(err.Error(), "AGENT_TOKEN") {
		t.Fatalf("legacy AGENT_TOKEN env should be rejected, got %v", err)
	}
	t.Setenv("AGENT_TOKEN", "")

	// 正常路径：自动生成 ID + 机器名并写回，再次读取保持稳定。
	cfg, err = LoadAgent(yamlPath)
	if err != nil {
		t.Fatal(err)
	}
	id1, err := EnsureAgentIdentity(cfg, yamlPath)
	if err != nil {
		t.Fatalf("ensure identity: %v", err)
	}
	if !strings.HasPrefix(id1, "agt_") || len(id1) != len("agt_")+32 {
		t.Fatalf("instance id shape mismatch: %q", id1)
	}
	reloaded, err := LoadAgent(yamlPath)
	if err != nil {
		t.Fatal(err)
	}
	id2, err := EnsureAgentIdentity(reloaded, yamlPath)
	if err != nil {
		t.Fatal(err)
	}
	if id1 != id2 {
		t.Fatalf("instance id must be stable across restarts: %q vs %q", id1, id2)
	}
	if reloaded.Agent.Name == "" {
		t.Fatal("machine name should be auto-filled")
	}
}

func TestDotEnvFindUpFromConfigDir(t *testing.T) {
	root := t.TempDir()
	yamlPath := filepath.Join(root, "a", "b", "agent.yaml")
	writeFile(t, yamlPath, "agent:\n  key: yaml-key\n")
	writeFile(t, filepath.Join(root, ".env"), "AGENT_KEY=cp_upfromroot\n")
	t.Setenv("AGENT_KEY", "")

	cfg, err := LoadAgent(yamlPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Agent.Key != "cp_upfromroot" {
		t.Errorf("Agent.Key = %q, want cp_upfromroot (应逐级向上找到 .env)", cfg.Agent.Key)
	}
}
