/**
 * 编译 Go 核心（codeporter-core）供 Electron 调用。
 *
 *   node scripts/build-core.mjs                   编译一次
 *   node scripts/build-core.mjs --skip-if-fresh   增量：没有任何 .go 源比产物新时跳过
 *   node scripts/build-core.mjs --watch           持续编译（开发用）
 *
 * 打包脚本默认走 --skip-if-fresh：界面/打包逻辑改了但 Go 没动时，直接复用上一次的
 * 核心产物，省掉一次完整链接。需要强制重建时不带该参数，或设 FORCE_CORE=1。
 *
 * 产物写到 backend/bin/，与 CoreProcess.candidates() 的查找路径一致。
 * 核心是「控制台子系统」而非 GUI 子系统：Electron 会以 stdio 管道拉起它，
 * 不存在双击场景，因此不需要 -H windowsgui（加了反而会让 stdout 失去意义）。
 */
import { spawn } from 'node:child_process'
import { existsSync, mkdirSync, readdirSync, rmSync, statSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const __dirname = path.dirname(fileURLToPath(import.meta.url))
const repoRoot = path.resolve(__dirname, '../..')
const backend = path.join(repoRoot, 'backend')
const outDir = path.join(backend, 'bin')

const isWin = process.platform === 'win32'
const outFile = path.join(outDir, isWin ? 'codeporter-core.exe' : 'codeporter-core')

/** 依次尝试可用的 go 命令。 */
function findGo() {
  const candidates = isWin ? ['go.exe', 'go'] : ['go']
  for (const dir of (process.env.PATH || '').split(path.delimiter)) {
    if (!dir) continue
    for (const name of candidates) {
      const p = path.join(dir, name)
      if (existsSync(p)) return p
    }
  }
  return null
}

/** 执行一次编译。 */
function build() {
  const go = findGo()
  if (!go) {
    console.error('[core] 未找到 go 命令，请安装 Go 1.23+ 或将其加入 PATH')
    process.exit(1)
  }
  mkdirSync(outDir, { recursive: true })
  // 真构建时先清掉旧产物：一个被截断/写坏的文件会让链接器报
  // "build output already exists and is not an object file"（增量跳过时不走到这）。
  if (existsSync(outFile)) rmSync(outFile, { force: true })
  console.log('[core] 编译 Go 核心 →', outFile)
  const p = spawn(go, ['build', '-trimpath', '-ldflags', '-s -w', '-o', outFile, './cmd/agent'], {
    cwd: backend,
    stdio: 'inherit',
    env: { ...process.env, GOPROXY: process.env.GOPROXY || 'https://goproxy.cn,direct' }
  })
  p.on('exit', (code) => {
    if (code === 0) {
      const kb = Math.round(statSync(outFile).size / 1024)
      console.log(`[core] 完成（${kb} KB）`)
      process.exit(0)
    }
    process.exit(code ?? 1)
  })
}

/** 监听 backend 下的 Go 变化并增量重编译（去抖）。 */
function watch() {
  console.log('[core] 监听 backend 变化…（开发模式）')
  let timer = null
  const trigger = () => {
    if (timer) clearTimeout(timer)
    timer = setTimeout(build, 400)
  }
  // 只扫一层目录即可覆盖本次改动范围，避免全量递归。
  const scan = () => {
    for (const entry of readdirSync(backend, { withFileTypes: true })) {
      if (entry.isDirectory() && ['cmd', 'internal', 'pkg'].includes(entry.name)) trigger()
    }
    for (const f of readdirSync(backend)) if (f === 'go.mod' || f === 'go.sum') trigger()
  }
  scan()
  // 简单起见做一次低频轮询，跨平台且无需额外依赖。
  setInterval(scan, 3000)
}

/**
 * 判断 Go 核心是否需要重编。
 *
 * 扫描 backend 下参与编译的源（cmd/internal/pkg 的 .go、go.mod/go.sum），
 * 取最新修改时间与现有产物比较；产物缺失、被强制要求、或有源更新时才返回 true。
 * 用 mtimeMs 数值比较，规避 cmd/不同区域设置下时间字符串排序不可靠的问题。
 */
function needsRebuild() {
  if (process.env.FORCE_CORE === '1') return true
  if (!existsSync(outFile)) return true
  const outMtime = statSync(outFile).mtimeMs
  let newest = 0
  const scanDir = (dir) => {
    let entries
    try {
      entries = readdirSync(dir, { withFileTypes: true })
    } catch {
      return
    }
    for (const e of entries) {
      const full = path.join(dir, e.name)
      if (e.isDirectory()) {
        if (e.name !== 'bin') scanDir(full)
      } else if (e.name.endsWith('.go')) {
        newest = Math.max(newest, statSync(full).mtimeMs)
      }
    }
  }
  for (const d of ['cmd', 'internal', 'pkg']) scanDir(path.join(backend, d))
  for (const f of ['go.mod', 'go.sum']) {
    const p = path.join(backend, f)
    if (existsSync(p)) newest = Math.max(newest, statSync(p).mtimeMs)
  }
  return newest > outMtime
}

const isWatch = process.argv.includes('--watch')
const skipIfFresh = process.argv.includes('--skip-if-fresh')

// 增量模式下产物仍是最新的就直接退出，不调用 go（省掉进程启动 + 链接）。
if (skipIfFresh && !isWatch && !needsRebuild()) {
  const kb = Math.round(statSync(outFile).size / 1024)
  console.log(`[core] 源码无变化，跳过编译，复用现有产物（${kb} KB）。设 FORCE_CORE=1 可强制重建。`)
  process.exit(0)
}

build()
if (isWatch) watch()
