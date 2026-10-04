#!/usr/bin/env bash
# ============================================================================
#  CodePorter Client - one-click build for macOS / Linux
#  （Windows 的 Git Bash 上也能跑：会自动识别成 win 平台）
#
#  用法：
#    bash build-client.sh          按当前系统完整构建并打包
#    bash build-client.sh deps     只装 npm 依赖
#    bash build-client.sh core     只编译 Go 核心
#    bash build-client.sh app      只构建 Electron 界面
#    bash build-client.sh pack     只打包（不重新编译）
#
#  可用环境变量覆盖：
#    PLATFORM=mac|linux|win        目标平台（默认按 uname 判断）
#    ARCH=x64|arm64                目标架构（默认按 uname -m 判断）
#    GOOS / GOARCH                 显式指定 Go 目标，默认由上面两者推导
#    USE_CN_MIRROR=0               不切国内镜像（CI 上建议设 0）
#    CSC_IDENTITY_AUTO_DISCOVERY=false   macOS 不签名（未配置证书时必须）
#
#  产物：client/release/
#    macOS  CodePorter-<ver>-mac-<arch>.dmg / .zip
#    Linux  CodePorter-<ver>-linux-<arch>.AppImage
#    Win    CodePorter-<ver>-portable.exe
#
#  注意：dmg 打包依赖 macOS 的 hdiutil/签名工具链，必须在 macOS 机器上执行，
#  无法在 Linux 或 Windows 上产出 dmg（要用 dmg 就走下面的 GitHub Actions）。
# ============================================================================
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CLIENT="$ROOT/client"
BACKEND="$ROOT/backend"
STEP="${1:-}"

log() { printf '%s\n' "$*"; }
warn() { printf '[WARN] %s\n' "$*" >&2; }
die() {
  printf '\n[ERROR] %s\n' "$*" >&2
  exit 1
}

# ---------- 平台与架构 ----------
UNAME_S="$(uname -s)"
UNAME_M="$(uname -m)"
case "$UNAME_S" in
Darwin) OS_DEFAULT=mac ;;
Linux) OS_DEFAULT=linux ;;
MINGW* | MSYS* | CYGWIN* | Windows_NT) OS_DEFAULT=win ;;
*) OS_DEFAULT="" ;;
esac
: "${PLATFORM:=$OS_DEFAULT}"
[ -n "$PLATFORM" ] || die "无法识别系统 '$UNAME_S'，请显式设置 PLATFORM=mac|linux|win。"

if [ -z "${ARCH:-}" ]; then
  case "$UNAME_M" in
  x86_64 | amd64) ARCH=x64 ;;
  arm64 | aarch64) ARCH=arm64 ;;
  *) die "无法识别 CPU '$UNAME_M'，请显式设置 ARCH=x64|arm64。" ;;
  esac
fi

case "$PLATFORM" in
mac) PLATFORM_FLAG=--mac; GOOS_DEFAULT=darwin ;;
linux) PLATFORM_FLAG=--linux; GOOS_DEFAULT=linux ;;
win) PLATFORM_FLAG=--win; GOOS_DEFAULT=windows ;;
*) die "PLATFORM 只能是 mac、linux 或 win（当前 '$PLATFORM'）。" ;;
esac

case "$ARCH" in
x64) GOARCH_DEFAULT=amd64 ;;
arm64) GOARCH_DEFAULT=arm64 ;;
universal) die "universal 需要把两个架构的核心 lipo 合并，暂不支持；请分别构建 x64 与 arm64。" ;;
*) die "ARCH 只能是 x64 或 arm64（当前 '$ARCH'）。" ;;
esac

: "${GOOS:=$GOOS_DEFAULT}"
: "${GOARCH:=$GOARCH_DEFAULT}"
export GOOS GOARCH
export CGO_ENABLED="${CGO_ENABLED:-0}"

CORE_NAME=codeporter-core
[ "$PLATFORM" = "win" ] && CORE_NAME=codeporter-core.exe
CORE_PATH="$BACKEND/bin/$CORE_NAME"

# ---------- 镜像（国内网络默认打开，CI 用 USE_CN_MIRROR=0 关掉） ----------
if [ "${USE_CN_MIRROR:-1}" = "1" ]; then
  : "${GOPROXY:=https://goproxy.cn,direct}"
  : "${ELECTRON_MIRROR:=https://npmmirror.com/mirrors/electron/}"
  : "${ELECTRON_BUILDER_BINARIES_MIRROR:=https://npmmirror.com/mirrors/electron-builder-binaries/}"
fi
# 只导出有值的：GOPROXY="" 会让 go 拒绝下载任何模块。
[ -n "${GOPROXY:-}" ] && export GOPROXY
[ -n "${ELECTRON_MIRROR:-}" ] && export ELECTRON_MIRROR
[ -n "${ELECTRON_BUILDER_BINARIES_MIRROR:-}" ] && export ELECTRON_BUILDER_BINARIES_MIRROR

# ---------- 坏掉的代理变量会让 electron-builder 的每次下载都以
#            "proxyconnect tcp: dial tcp :0" 失败，先清掉 ----------
sanitize_proxy() {
  local name="$1" val="${!1:-}"
  [ -n "$val" ] || return 0
  case "$val" in
  *://*:[1-9]*) return 0 ;;
  esac
  warn "$name=\"$val\" 不是可用的代理地址，本次构建忽略它。"
  unset "$name"
}
for v in HTTP_PROXY HTTPS_PROXY http_proxy https_proxy ALL_PROXY all_proxy; do
  sanitize_proxy "$v"
done

log ""
log "============================================================"
log " CodePorter Client Build"
log " Repo:     $ROOT"
log " Platform: $PLATFORM / $ARCH  (GOOS=$GOOS GOARCH=$GOARCH)"
log "============================================================"
log ""

# ---------- 0. 前置工具 ----------
command -v go >/dev/null 2>&1 || die "未找到 go，请安装 Go 1.23+ 并加入 PATH。"
command -v node >/dev/null 2>&1 || die "未找到 node，请安装 Node.js 20.19+ 并加入 PATH。"
log "[1/5] 环境 OK: $(go version) | Node $(node -v)"

# ---------- 1. npm 依赖 ----------
if [ "$STEP" = "pack" ]; then
  log "[2/5] 跳过依赖安装（pack 模式）。"
elif [ -d "$CLIENT/node_modules" ]; then
  log "[2/5] 依赖已安装，跳过。"
else
  log "[2/5] 安装 npm 依赖（首次较慢）..."
  (cd "$CLIENT" && npm install --no-audit --no-fund)
fi

# ---------- 2. Electron 运行时 ----------
# npm 的 allow-scripts 可能拦掉 electron 的 postinstall，导致没有二进制。
if [ ! -d "$CLIENT/node_modules/electron/dist" ]; then
  log "[2.5/5] 下载 Electron 运行时..."
  (cd "$CLIENT" && node node_modules/electron/install.js)
fi

# ---------- 3. Go 核心 ----------
log "[3/5] 编译 Go 核心..."
# 先删旧产物：残留的半截文件会让链接器报
# "build output already exists and is not an object file" 这种莫名其妙的错。
rm -f "$CORE_PATH"
(cd "$BACKEND" && go build -trimpath -ldflags "-s -w" -o "bin/$CORE_NAME" ./cmd/agent)

# 大小校验：核心被截断/覆盖会表现为几字节，打包出来能启动但一调用就报
# "核心进程未启动"，在这里拦住比让用户自己去猜强。
[ -f "$CORE_PATH" ] || die "编译后找不到 $CORE_PATH。"
file_size() {
  if stat -c%s "$1" >/dev/null 2>&1; then
    stat -c%s "$1" # GNU coreutils (Linux)
  else
    stat -f%z "$1" # BSD (macOS)
  fi
}
CORE_SIZE="$(file_size "$CORE_PATH")"
if [ "$CORE_SIZE" -lt 1048576 ]; then
  die "核心只有 ${CORE_SIZE} 字节，明显被截断/覆盖（或被安全软件清空），请检查后重建。"
fi
log "      核心: backend/bin/$CORE_NAME (${CORE_SIZE} bytes)"

[ "$STEP" = "core" ] && exit 0
if [ "$STEP" = "pack" ]; then
  log "[4/5] 跳过界面构建（pack 模式）。"
else
  # ---------- 4. Electron 界面 ----------
  log "[4/5] 构建 Electron 主进程与渲染进程..."
  (cd "$CLIENT" && npm run build)
  [ "$STEP" = "app" ] && exit 0
fi

# ---------- 5. 打包 ----------
# npm 里残留的 proxy=null 会被 electron-builder 当成真代理传下去。
for k in proxy https-proxy; do
  if [ "$(npm config get "$k" 2>/dev/null | tr -d '[:space:]')" = "null" ]; then
    npm config delete "$k"
  fi
done

log "[5/5] 打包 $PLATFORM/$ARCH ..."
(cd "$CLIENT" && npx electron-builder "$PLATFORM_FLAG" "--$ARCH" --config electron-builder.config.cjs)

# 打包进 app 的核心同样可能被清空，这里再验一次。
PACKED="$CLIENT/release"
case "$PLATFORM" in
mac) PACKED="$CLIENT/release/mac*/CodePorter.app/Contents/Resources/core/$CORE_NAME" ;;
linux) PACKED="$CLIENT/release/linux*/resources/core/$CORE_NAME" ;;
win) PACKED="$CLIENT/release/win-unpacked/resources/core/$CORE_NAME" ;;
esac
for f in $PACKED; do
  [ -f "$f" ] || continue
  s="$(file_size "$f")"
  if [ "$s" -lt 1048576 ]; then
    warn "打进包里的核心只有 ${s} 字节（$f），多半被安全软件清空。"
  fi
done

log ""
log "============================================================"
log " BUILD OK"
log "============================================================"
if [ -d "$CLIENT/release" ]; then
  find "$CLIENT/release" -maxdepth 2 \( -name '*.dmg' -o -name '*.zip' -o -name '*.AppImage' -o -name '*.exe' \) \
    -printf '    %f    %s bytes\n' 2>/dev/null ||
    ls -la "$CLIENT/release"
fi
log ""
log "产物目录: $CLIENT/release"
if [ "$PLATFORM" = "mac" ]; then
  log "未签名的 dmg 会被 Gatekeeper 拦，首次打开请右键 -> 打开，或执行："
  log "  xattr -cr /Applications/CodePorter.app"
fi
log ""
