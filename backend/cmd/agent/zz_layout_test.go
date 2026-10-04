//go:build windows

package main

import (
	"testing"
	"unsafe"
)

// TestOpenFileNameWLayout 校验结构体布局与 Win32 SDK 一致。
// 布局错了会向系统 API 传错内存，属于必须在编译期之外拦住的错误。
func TestOpenFileNameWLayout(t *testing.T) {
	var ofn openFileNameW
	if got := unsafe.Sizeof(ofn); got != expectOpenFileNameSize() {
		t.Fatalf("OPENFILENAMEW 大小 = %d，期望 %d", got, expectOpenFileNameSize())
	}
	// 关键字段偏移（x64 / x86）：lpstrFile 与 Flags 决定了 API 读写的正确性。
	ptr := unsafe.Sizeof(uintptr(0))
	wantFileOff := uintptr(0)
	if ptr == 8 {
		wantFileOff = 32
	} else {
		wantFileOff = 16
	}
	if got := unsafe.Offsetof(ofn.lpstrFile); got != wantFileOff {
		t.Errorf("lpstrFile 偏移 = %d，期望 %d", got, wantFileOff)
	}
	if got := unsafe.Offsetof(ofn.Flags); got != wantFileOff+40 {
		t.Errorf("Flags 偏移 = %d，期望 %d", got, wantFileOff+40)
	}
	t.Logf("OPENFILENAMEW 布局正确：大小 %d 字节，lpstrFile 偏移 %d", unsafe.Sizeof(ofn), wantFileOff)
}
