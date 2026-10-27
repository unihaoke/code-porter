package port

import (
	"context"
	"net/http"
)

// HTTPClient 出站 HTTP 调用端口，由基础设施层（net/http.Client）或测试替身实现。
//
// 回调分发器通过该端口向调用方 callback_url 发送 POST，避免应用层直接耦合
// net/http.Client，便于在测试中注入假实现。
type HTTPClient interface {
	// Do 执行 HTTP 请求。
	Do(req *http.Request) (*http.Response, error)
}

// ctxKey 避免与其他包的 context key 冲突。
type ctxKey struct{}

// WithHTTPClient 将 HTTPClient 注入 context，供基础设施层按需取用。
func WithHTTPClient(ctx context.Context, c HTTPClient) context.Context {
	return context.WithValue(ctx, ctxKey{}, c)
}

// HTTPClientFromContext 从 context 取出 HTTPClient；不存在返回 nil。
func HTTPClientFromContext(ctx context.Context) HTTPClient {
	v, _ := ctx.Value(ctxKey{}).(HTTPClient)
	return v
}
