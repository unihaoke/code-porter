//go:build !windows

package main

import "errors"

// runGUI 图形界面仅支持 Windows；其他平台请使用 -console 命令行模式。
func runGUI(configPath string) error {
	return errors.New("图形界面仅支持 Windows，请使用 -console 以命令行模式运行")
}
