// Package version 提供构建期注入的版本信息。
//
// 发布脚本 scripts/release 通过 -ldflags "-X github.com/codeporter/code-porter/pkg/version.Version=vX.Y.Z"
// 注入具体值；本地直接 go build 时回落到下面的默认值。
package version

import (
	"runtime"
	"strings"
)

// 构建期可注入变量。
var (
	// Version 语义化版本号。
	Version = "v0.2.0-dev"
	// Commit 构建对应的 git 提交号（短哈希）。
	Commit = "unknown"
	// BuildDate 构建时间（RFC3339）。
	BuildDate = "unknown"
)

// Info 描述一次构建的完整信息。
type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildDate string `json:"build_date"`
	GoVersion string `json:"go_version"`
	Platform  string `json:"platform"`
}

// Get 返回当前二进制的版本信息。
func Get() Info {
	return Info{
		Version:   Version,
		Commit:    Commit,
		BuildDate: BuildDate,
		GoVersion: strings.TrimPrefix(runtime.Version(), "go"),
		Platform:  runtime.GOOS + "/" + runtime.GOARCH,
	}
}

// String 返回单行可读的版本描述。
func String() string {
	i := Get()
	s := i.Version
	if i.Commit != "" && i.Commit != "unknown" {
		s += " (" + i.Commit + ")"
	}
	return s + " built at " + i.BuildDate + " with go" + i.GoVersion + " for " + i.Platform
}
