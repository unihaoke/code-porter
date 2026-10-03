package http

import (
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// SPAHandler 托管前端单页应用的构建产物。
//
// 规则：能命中真实文件就直接返回（带正确 Content-Type），
// 否则回退到 index.html 交给前端路由处理——这是 Vue Router history 模式的标准做法。
type SPAHandler struct {
	root     string
	fallback string
	fs       http.Handler
}

// NewSPAHandler 构造处理器；dir 为空或目录不存在时返回 nil（调用方据此关闭托管）。
func NewSPAHandler(dir string) *SPAHandler {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return nil
	}
	return &SPAHandler{
		root:     dir,
		fallback: filepath.Join(dir, "index.html"),
		fs:       http.FileServer(http.Dir(dir)),
	}
}

// ServeHTTP 服务静态资源或回退到首页。
func (h *SPAHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	upath := r.URL.Path
	if !strings.HasPrefix(upath, "/") {
		upath = "/" + upath
	}
	clean := path.Clean(upath)
	if clean == "/" {
		http.ServeFile(w, r, h.fallback)
		return
	}
	// 命中真实文件（且不是目录）则直接交给 FileServer。
	target := filepath.Join(h.root, filepath.FromSlash(clean))
	if info, err := os.Stat(target); err == nil && !info.IsDir() {
		h.fs.ServeHTTP(w, r)
		return
	}
	http.ServeFile(w, r, h.fallback)
}
