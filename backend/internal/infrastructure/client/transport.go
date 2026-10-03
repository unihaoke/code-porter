package client

import (
	"crypto/tls"
	"net"
	"net/http"
	"time"
)

// newTransport 构造带连接池的 HTTP Transport。
//
// 复用连接 + 合理的空闲连接数，避免 Pull 高频轮询时反复 TLS 握手。
func newTransport(insecure bool) http.RoundTripper {
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:          64,
		MaxIdleConnsPerHost:   8,
		MaxConnsPerHost:       32,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
		ForceAttemptHTTP2:     true,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: insecure, //nolint:gosec // 自签证书场景由配置显式开启
			MinVersion:         tls.VersionTLS12,
		},
	}
}
