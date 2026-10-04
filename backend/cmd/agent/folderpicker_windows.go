//go:build windows

package main

import (
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows 目录选择对话框。
//
// 选用 GetOpenFileNameW + OFN_PICKFOLDERS，而非 SHBrowseForFolderW：
// 前者不需要 COM 初始化、不返回需要手动释放的 PIDL，也没有 uintptr→unsafe.Pointer
// 的风险转换（go vet 会拦截那类写法），实现更短更安全。
// 代价是对话框是经典样式（可接受）。

var (
	modComdlg32          = windows.NewLazySystemDLL("comdlg32.dll")
	procGetOpenFileNameW = modComdlg32.NewProc("GetOpenFileNameW")
)

// GetOpenFileNameW 的标志位。
const (
	ofnExplorer      = 0x00080000 // 新版「浏览」风格
	ofnPathMustExist = 0x00000800 // 必须是已存在��路径
	ofnFileMustExist = 0x00001000 // 必须是已存在的文件
	ofnPickFolders   = 0x00000020 // 切换为「选择文件夹」模式
	ofnNoChangeDir   = 0x00000008 // 不改变进程当前目录
	ofnHideReadOnly  = 0x00000004 // 隐藏「只读」复选框
	ofnSetTitleText  = 0x08000000 // 把 lpstrTitle 当作对话框标题
)

// maxPathBuf 目录路径缓冲区长度（含结尾 NUL）。
const maxPathBuf = 260

// openFileNameW 对应 Win32 的 OPENFILENAMEW。
//
// 字段顺序、类型与官方定义一一对应；结构体总大小与关键字段偏移都会做校验，
// 布局写错时直接报错，而不是把错乱的内存交给系统 API。
type openFileNameW struct {
	LStructSize       uint32
	HWndOwner         windows.Handle
	HInstance         windows.Handle
	lpstrFilter       *uint16
	lpstrFile         *uint16
	lpstrCustomFilter *uint16
	NMaxCustFilter    uint32
	NFilterIndex      uint32
	lpstrInitialDir   *uint16
	lpstrTitle        *uint16
	Flags             uint32
	NFileOffset       uint16
	NFileExtension    uint16
	lpstrDefExt       *uint16
	lCustData         uintptr
	lpfnHook          uintptr
	lpTemplateName    *uint16
	pvReserved        uintptr
	dwReserved        uint32
	pvFileInfo        uintptr
	FlagsEx           uint32
}

// expectOpenFileNameSize 该结构在当前字长下的期望字节数。
// 数值取自 Win32 SDK 的实际布局：x64 = 144，x86 = 80。
func expectOpenFileNameSize() uintptr {
	if unsafe.Sizeof(uintptr(0)) == 8 {
		return 144
	}
	return 80
}

// pickFolder 弹出系统目录选择对话框。
// owner 为父窗口句柄（传 0 表示无 owner）；title 为对话框标题；
// initialDir 为对话框打开时预先填入的目录（传空则用系统默认）。
// 用户取消时返回空字符串与 nil error。
func pickFolder(owner windows.Handle, title, initialDir string) (string, error) {
	pszTitle, err := windows.UTF16PtrFromString(title)
	if err != nil {
		return "", fmt.Errorf("目录对话框标题无效: %w", err)
	}

	// 文件名缓冲区：既是输入（初始目录）也是输出（所选路径）。
	buf := make([]uint16, maxPathBuf)
	if initialDir != "" {
		// 预先填入当前值；超长或非法时忽略即可，回落到默认位置。
		if src, err := windows.UTF16FromString(initialDir); err == nil && len(src) < maxPathBuf {
			copy(buf, src)
		}
	}

	ofn := openFileNameW{
		LStructSize: uint32(unsafe.Sizeof(openFileNameW{})),
		HWndOwner:   owner,
		lpstrFile:   &buf[0],
		lpstrTitle:  pszTitle,
		Flags: ofnExplorer | ofnPathMustExist | ofnFileMustExist |
			ofnPickFolders | ofnNoChangeDir | ofnHideReadOnly | ofnSetTitleText,
	}
	if got := unsafe.Sizeof(ofn); got != expectOpenFileNameSize() {
		return "", fmt.Errorf("OPENFILENAMEW 结构尺寸异常: %d（期望 %d）", got, expectOpenFileNameSize())
	}

	ret, _, _ := procGetOpenFileNameW.Call(uintptr(unsafe.Pointer(&ofn)))
	if ret == 0 {
		// 返回 0 一律按「用户取消」处理。
		//
		// 不要用 CommDlgExtendedError 区分：它只适用于旧式对话框，在
		// OFN_EXPLORER 模式下返回的是上次调用的残留值，会把用户正常的
		// 「取消」误报成「打开目录对话框失败，错误码 0x…」。

		// 仍记录 GetLastError 便于真出问题时排查，但不影响交互。
		if errno := windows.GetLastError(); errno != nil {
			fmt.Fprintf(os.Stderr, "[folderpicker] GetOpenFileNameW 返回 0, GetLastError=%v\n", errno)
		}
		return "", nil
	}
	return windows.UTF16ToString(buf), nil
}
