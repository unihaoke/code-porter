package config

// .env 文件解析（OS env > .env 文件 > yaml 的前两级查找机制）。

import (
	"bufio"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// dotEnvValues 解析出的 .env 键值。
type dotEnvValues map[string]string

// lookup 解析环境变量：OS env（OS env，OS env，docker compose / 手动 export）> .env 文件 > ""。
func (d dotEnvValues) lookup(name string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return d[name]
}

// loadDotEnv 定位并解析 .env 文件；找不到返回空 map。
func loadDotEnv(configPath string) dotEnvValues {
	dir := filepath.Dir(configPath)
	if start := filepath.Dir(configPath); start != "" && start != "." {
		dir = start
	}
	p := findDotEnv(dir, 8)
	if p == "" {
		return nil
	}
	vals, err := parseDotEnvFile(p)
	if err != nil {
		return nil
	}
	return vals
}

// findDotEnv 从起点目录逐级向上（含当前）找 .env。
func findDotEnv(start string, maxDepth int) string {
	dir := start
	for i := 0; i < maxDepth; i++ {
		candidate := filepath.Join(dir, ".env")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

// parseDotEnvFile 解析单行 KEY=VALUE 的 .env 文件。
//
// 规则（与常见 dotEnv 工具一致）：
//   - 跳过空行与 # 注释行
//   - 第一个 = 分隔键值
//   - 值去首尾空白
//   - 值支持 "xxx"、'xxx' 引号
//   - 行内注释只在值未加引号且以空格 # 结尾时才视为注释（保守处理）
func parseDotEnvFile(path string) (dotEnvValues, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	out := dotEnvValues{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])

		if len(val) >= 2 {
			switch val[0] {
			case '"', '\'':
				if byte(val[len(val)-1]) == val[0] {
					val = val[1 : len(val)-1]
				}
			}
		}
		out[key] = val
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

var _ = errors.Is
