// Command agent 启动 CodePorter 本地代理（LocalAgent）。
//
// 职责：主动出站连接网关 → 拉取/接收任务 → 本地协程池限流 → 调用本机 MCP AI 工具 → 回传结果。
// 本机不监听任何端口，外网无法主动访问，安全性由「出站连接」保证。
//
// 运行模式：
//   - 默认：无界面守护进程（服务、容器、命令行）；
//   - -ipc：作为 Electron 图形客户端的 Go 核心，经 stdin/stdout JSON 行协议驱动；
//   - -test-cli：本机 AI 工具连通性自检。
//
// Windows 用户如需图形界面，请使用 Electron 客户端（client/ 目录的发布包）。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/codeporter/code-porter/internal/infrastructure/config"
	"github.com/codeporter/code-porter/internal/infrastructure/logging"
	"github.com/codeporter/code-porter/pkg/version"
)

func main() {
	configPath := flag.String("config", "configs/agent.yaml", "agent config file path")
	showVersion := flag.Bool("version", false, "print version and exit")
	testCLI := flag.Bool("test-cli", false, "test local AI CLI connectivity and exit")
	noProbe := flag.Bool("no-probe", false, "with -test-cli: only check installation, do not issue a real call")
	ipcMode := flag.Bool("ipc", false, "run as an IPC core driven by the Electron client over stdin/stdout JSON lines")
	flag.Parse()

	cfgPath := resolveConfigPath(*configPath)

	if *showVersion {
		fmt.Println("codeporter-agent " + version.String())
		return
	}

	// IPC 模式：供 Electron 图形客户端驱动。必须在其他分支之前判断。
	if *ipcMode {
		cfg, err := loadConfig(cfgPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "load config: %v\n", err)
			os.Exit(1)
		}
		bootLog := logging.New(os.Stderr, logging.ParseLevel(cfg.Log.Level))
		if err := newIPCServer(cfgPath, cfg, bootLog).run(context.Background()); err != nil {
			fmt.Fprintf(os.Stderr, "ipc exited: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// 本地 AI 连通性自检。
	if *testCLI {
		os.Exit(runCLITest(cfgPath, *noProbe))
	}

	// 默认无界面守护模式。
	if err := runHeadless(cfgPath); err != nil {
		fmt.Fprintf(os.Stderr, "agent exited with error: %v\n", err)
		os.Exit(1)
	}
}

// resolveConfigPath 把相对路径解析为「可执行文件所在目录」下的绝对路径，
// 这样双击 exe 时无论系统起始目录如何都能正确找到 configs/agent.yaml。
func resolveConfigPath(p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	if _, err := os.Stat(p); err == nil {
		if abs, err := filepath.Abs(p); err == nil {
			return abs
		}
	}
	if exe, err := os.Executable(); err == nil {
		if candidate := filepath.Join(filepath.Dir(exe), p); true {
			if _, err := os.Stat(candidate); err == nil {
				return candidate
			}
		}
	}
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

// loadConfig 加载配置；文件不存在时回落到默认配置（IPC 客户端会据此引导用户创建新文件）。
func loadConfig(path string) (*config.AgentConfig, error) {
	cfg, err := config.LoadAgent(path)
	if err != nil {
		if errors.Is(err, config.ErrNotFound) {
			return config.LoadAgent("")
		}
		return nil, err
	}
	return cfg, nil
}

// runHeadless 无界面模式：启动网关代理；配置中启用的 IM 机器人也一并启动。
// 阻塞直到收到终止信号后优雅停止两者。
func runHeadless(configPath string) error {
	cfg, err := loadConfig(configPath)
	if err != nil {
		return err
	}
	log := logging.New(os.Stdout, logging.ParseLevel(cfg.Log.Level))
	svc := NewService(configPath, cfg, log)
	if err := svc.Start(); err != nil {
		return err
	}
	if cfg.Bots.Feishu.Enabled {
		if err := svc.StartBots(); err != nil {
			log.Warn("feishu bot not started: " + err.Error())
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
	log.Info("shutting down")
	svc.StopBots()
	svc.Stop()
	return nil
}

// runCLITest 执行本地 AI 连通性自检并打印报告，返回进程退出码。
func runCLITest(configPath string, skipProbe bool) int {
	cfg, err := loadConfig(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "加载配置失败: %v\n", err)
		return 1
	}
	log := logging.New(os.Stdout, logging.ParseLevel(cfg.Log.Level))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	results := TestCLIConnections(ctx, cfg, log, CLIConnTestOptions{
		SkipProbe: skipProbe,
		Timeout:   90 * time.Second,
	})
	fmt.Print(FormatCLIConnReport(results))

	ok, missing, warned, failed := summarizeCLIConn(results)
	fmt.Printf("\n汇总: 可用 %d · 未安装 %d · 需处理 %d · 失败 %d\n", ok, missing, warned, failed)
	if warned > 0 {
		fmt.Println("提示: 「额度/配额不足」说明调用链已通、认证已通过，只是账号额度用尽 —— 无需配置 API 密钥。")
	}
	if ok == 0 && (missing > 0 || failed > 0) {
		return 1
	}
	return 0
}
