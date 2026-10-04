// Package mcp 实现 MCP（Model Context Protocol）适配层：统一封装对本地 AI 编码工具的调用。
//
// MVP 通过 MCP stdio 传输与本地 AI 软件（Trae / Claude Code / CodeBuddy / Codex）通信：
// 启动本机 MCP Server 子进程 → JSON-RPC 握手 → 调用工具 → 收集流式输出。
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

// rpcMessage 统一的 JSON-RPC 报文（响应与通知共用）。
type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e *rpcError) Error() string {
	return fmt.Sprintf("mcp error %d: %s", e.Code, e.Message)
}

// rpcRequest 请求报文。
type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// NotifyHandler 服务端通知回调（用于接收流式增量）。
type NotifyHandler func(method string, params json.RawMessage)

// StdioConfig MCP stdio 子进程配置。
type StdioConfig struct {
	// Command 可执行命令，如 trae / claude / npx。
	Command string
	// Args 命令参数。
	Args []string
	// Env 附加环境变量。
	Env map[string]string
	// WorkDir 工作目录。
	WorkDir string
	// StartupTimeout 启动与初始化超时。
	StartupTimeout time.Duration
	// MaxLineBytes 单行 JSON 最大字节数。
	MaxLineBytes int
}

func (c StdioConfig) withDefaults() StdioConfig {
	if c.StartupTimeout <= 0 {
		c.StartupTimeout = 30 * time.Second
	}
	if c.MaxLineBytes <= 0 {
		c.MaxLineBytes = 8 << 20
	}
	return c
}

// StdioClient MCP stdio 客户端：一个子进程对应一个 MCP Server 会话。
type StdioClient struct {
	cfg    StdioConfig
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stderr io.ReadCloser

	mu      sync.Mutex
	nextID  atomic.Int64
	pending map[int64]chan *rpcMessage
	notify  []NotifyHandler
	fail    chan error

	started   atomic.Bool
	closed    atomic.Bool
	closeOnce sync.Once
}

// NewStdioClient 构造客户端（尚未启动子进程）。
func NewStdioClient(cfg StdioConfig) *StdioClient {
	cfg = cfg.withDefaults()
	return &StdioClient{
		cfg:     cfg,
		pending: make(map[int64]chan *rpcMessage),
		fail:    make(chan error, 1),
	}
}

// Start 启动子进程并完成 MCP 初始化握手。
func (c *StdioClient) Start(ctx context.Context) error {
	if c.started.Load() {
		return nil
	}
	if c.cfg.Command == "" {
		return errors.New("mcp command is not configured")
	}
	cmd := exec.Command(c.cfg.Command, c.cfg.Args...)
	cmd.SysProcAttr = stdioSysProcAttr()
	cmd.Env = os.Environ()
	for k, v := range c.cfg.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	if c.cfg.WorkDir != "" {
		cmd.Dir = c.cfg.WorkDir
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start mcp server %q: %w", c.cfg.Command, err)
	}

	c.cmd = cmd
	c.stdin = stdin
	c.stderr = stderr
	go c.readLoop(bufio.NewReaderSize(stdout, 64*1024))
	go c.drainStderr()

	initCtx, cancel := context.WithTimeout(ctx, c.cfg.StartupTimeout)
	defer cancel()

	var initParams = map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "codeporter", "version": "0.2.0"},
	}
	if _, err := c.Call(initCtx, "initialize", initParams); err != nil {
		return fmt.Errorf("mcp initialize failed: %w", err)
	}
	// 初始化完成通知（无 id，不等待响应）。
	_ = c.notifyServer("notifications/initialized", map[string]any{})
	c.started.Store(true)
	return nil
}

// Call 发起一次 JSON-RPC 调用并等待响应。
func (c *StdioClient) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if c.closed.Load() {
		return nil, errors.New("mcp client closed")
	}
	id := c.nextID.Add(1)
	ch := make(chan *rpcMessage, 1)

	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()

	req := rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}
	raw, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	_, werr := c.stdin.Write(append(raw, '\n'))
	c.mu.Unlock()
	if werr != nil {
		return nil, fmt.Errorf("write to mcp server: %w", werr)
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case err := <-c.fail:
		return nil, err
	case msg := <-ch:
		if msg.Error != nil {
			return nil, msg.Error
		}
		return msg.Result, nil
	}
}

// OnNotify 注册服务端通知回调，返回取消注册函数。
func (c *StdioClient) OnNotify(h NotifyHandler) func() {
	c.mu.Lock()
	idx := len(c.notify)
	c.notify = append(c.notify, h)
	c.mu.Unlock()
	return func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		if idx < len(c.notify) {
			c.notify = append(c.notify[:idx], c.notify[idx+1:]...)
		}
	}
}

func (c *StdioClient) notifyServer(method string, params any) error {
	req := map[string]any{"jsonrpc": "2.0", "method": method, "params": params}
	raw, err := json.Marshal(req)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err = c.stdin.Write(append(raw, '\n'))
	return err
}

func (c *StdioClient) readLoop(r *bufio.Reader) {
	dec := json.NewDecoder(r)
	dec.UseNumber()
	for {
		var msg rpcMessage
		if err := dec.Decode(&msg); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrClosedPipe) {
				select {
				case c.fail <- errors.New("mcp server closed the connection"):
				default:
				}
				return
			}
			select {
			case c.fail <- fmt.Errorf("decode mcp message: %w", err):
			default:
			}
			return
		}
		if msg.ID != nil {
			c.mu.Lock()
			ch, ok := c.pending[*msg.ID]
			c.mu.Unlock()
			if ok {
				select {
				case ch <- &msg:
				default:
				}
			}
			continue
		}
		// 服务端主动通知：分发给订阅者（流式增量）。
		c.mu.Lock()
		handlers := append([]NotifyHandler(nil), c.notify...)
		c.mu.Unlock()
		for _, h := range handlers {
			h(msg.Method, msg.Params)
		}
	}
}

func (c *StdioClient) drainStderr() {
	if c.stderr == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(c.stderr, 1<<20))
}

// Close 终止子进程。
func (c *StdioClient) Close() error {
	var err error
	c.closeOnce.Do(func() {
		c.closed.Store(true)
		if c.stdin != nil {
			_ = c.stdin.Close()
		}
		if c.cmd != nil && c.cmd.Process != nil {
			_ = c.cmd.Process.Kill()
			_, err = c.cmd.Process.Wait()
		}
	})
	return err
}
