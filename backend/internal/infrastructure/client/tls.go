package client

import "crypto/tls"

// tlsConfig 构造 TLS 配置。
func tlsConfig(insecure bool) *tls.Config {
	return &tls.Config{
		InsecureSkipVerify: insecure, //nolint:gosec // 自签证书场景由配置显式开启
		MinVersion:         tls.VersionTLS12,
	}
}
