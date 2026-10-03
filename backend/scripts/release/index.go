package main

import (
	"html"
	"os"
	"strings"
	"time"
)

// writeIndexHTML 生成一个可直接托管的静态下载页。
//
// 页面自带平台识别：访客打开后会自动高亮与自己系统匹配的包。
func writeIndexHTML(path, project, relVersion, commit, buildDate string, artifacts []artifact) error {
	var b strings.Builder
	b.WriteString(htmlHead(project, relVersion))
	b.WriteString(htmlHero(project, relVersion, commit, buildDate))

	for _, group := range []struct {
		kind  string
		title string
		desc  string
	}{
		{"agent", "本地客户端 · LocalAgent", "安装在你自己的开发机上。只向网关发起出站连接，不开放任何入站端口。"},
		{"gateway", "网关服务 · Gateway", "部署在公网服务器上。对外提供 OpenAI 兼容接口，负责任务调度与结果汇聚。"},
	} {
		items := filterBy(artifacts, group.kind)
		if len(items) == 0 {
			continue
		}
		b.WriteString(htmlSection(group.title, group.desc, items))
	}

	b.WriteString(htmlFooter(relVersion, artifacts))
	return writeTextFile(path, b.String())
}

func htmlHead(project, relVersion string) string {
	return `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>` + esc(project) + ` 客户端下载 · ` + esc(relVersion) + `</title>
<style>
  :root {
    --bg: #f6f7f9;
    --card: #ffffff;
    --border: #e3e6ea;
    --text: #1c1f23;
    --muted: #6b7280;
    --brand: #2f6feb;
    --brand-soft: #eaf1fe;
    --ok: #1a7f37;
    --ok-soft: #e7f6ec;
    --code-bg: #f3f4f6;
  }
  * { box-sizing: border-box; }
  body {
    margin: 0; padding: 40px 20px 80px;
    background: var(--bg); color: var(--text);
    font: 15px/1.65 -apple-system, BlinkMacSystemFont, "Segoe UI", "PingFang SC",
          "Hiragino Sans GB", "Microsoft YaHei", sans-serif;
  }
  .wrap { max-width: 940px; margin: 0 auto; }
  header { margin-bottom: 36px; }
  h1 { margin: 0 0 10px; font-size: 28px; letter-spacing: -0.02em; }
  .sub { color: var(--muted); font-size: 14px; }
  .badges { margin-top: 14px; display: flex; gap: 8px; flex-wrap: wrap; }
  .badge {
    display: inline-block; padding: 3px 10px; border-radius: 999px;
    background: var(--code-bg); color: var(--muted);
    font-size: 12px; font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  }
  .badge.brand { background: var(--brand-soft); color: var(--brand); }
  h2 { font-size: 18px; margin: 40px 0 6px; }
  .desc { color: var(--muted); font-size: 14px; margin: 0 0 18px; }
  .grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(280px, 1fr)); gap: 14px; }
  .card {
    background: var(--card); border: 1px solid var(--border); border-radius: 12px;
    padding: 16px 18px; position: relative;
  }
  .card.recommended { border-color: var(--brand); box-shadow: 0 0 0 3px var(--brand-soft); }
  .tag {
    position: absolute; top: -9px; right: 12px; background: var(--brand); color: #fff;
    font-size: 11px; padding: 2px 8px; border-radius: 999px;
  }
  .os { font-weight: 600; font-size: 15px; margin: 0 0 2px; }
  .file {
    font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
    font-size: 12px; color: var(--muted); word-break: break-all; margin: 0 0 12px;
  }
  .meta { font-size: 12px; color: var(--muted); margin: 0 0 12px; }
  a.btn {
    display: inline-block; text-decoration: none; background: var(--brand); color: #fff;
    padding: 7px 16px; border-radius: 8px; font-size: 14px; font-weight: 500;
  }
  a.btn:hover { filter: brightness(1.08); }
  details { margin-top: 12px; }
  summary { cursor: pointer; font-size: 12px; color: var(--muted); user-select: none; }
  .sum {
    margin-top: 8px; font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
    font-size: 11px; color: var(--muted); word-break: break-all; background: var(--code-bg);
    padding: 8px 10px; border-radius: 6px;
  }
  pre {
    background: var(--code-bg); padding: 14px 16px; border-radius: 10px; overflow-x: auto;
    font-size: 13px; line-height: 1.7;
  }
  code { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
  footer { margin-top: 48px; padding-top: 20px; border-top: 1px solid var(--border); font-size: 13px; color: var(--muted); }
  footer a { color: var(--brand); text-decoration: none; }
  #hint { margin-top: 4px; font-size: 13px; color: var(--ok); min-height: 20px; }
</style>
</head>
<body>
<div class="wrap">
`
}

func htmlHero(project, relVersion, commit, buildDate string) string {
	return `<header>
  <h1>` + esc(project) + ` 客户端下载</h1>
  <div class="sub">选择与你系统匹配的包，下载后修改配置即可接入网关。</div>
  <div class="badges">
    <span class="badge brand">` + esc(relVersion) + `</span>
    <span class="badge">commit ` + esc(commit) + `</span>
    <span class="badge">` + esc(buildDate) + `</span>
    <span class="badge">静态链接 · 无运行时依赖</span>
  </div>
  <div id="hint"></div>
</header>
`
}

func htmlSection(title, desc string, items []artifact) string {
	var b strings.Builder
	b.WriteString("<h2>" + esc(title) + "</h2>\n")
	b.WriteString(`<p class="desc">` + esc(desc) + "</p>\n")
	b.WriteString(`<div class="grid">` + "\n")
	for _, a := range items {
		b.WriteString(`  <div class="card" data-goos="` + a.GOOS + `" data-goarch="` + a.GOARCH + `">` + "\n")
		b.WriteString("    <p class=\"os\">" + esc(a.Label) + "</p>\n")
		b.WriteString("    <p class=\"file\">" + esc(a.File) + "</p>\n")
		b.WriteString("    <p class=\"meta\">" + a.SizeMB + " · " + a.GOOS + "/" + a.GOARCH + "</p>\n")
		b.WriteString("    <a class=\"btn\" href=\"./" + attr(a.File) + "\">下载</a>\n")
		b.WriteString("    <details><summary>SHA256 校验值</summary><div class=\"sum\">" + a.SHA256 + "</div></details>\n")
		b.WriteString("  </div>\n")
	}
	b.WriteString("</div>\n")
	return b.String()
}

func htmlFooter(relVersion string, artifacts []artifact) string {
	agent := filterBy(artifacts, "agent")
	example := "codeporter-agent-" + relVersion + "-linux-amd64.tar.gz"
	if len(agent) > 0 {
		example = agent[0].File
	}
	return `<h2>校验与快速开始</h2>
<p class="desc">下载后建议核对校验值，防止包被篡改。</p>
<pre><code># 核对校验值
sha256sum -c SHA256SUMS.txt

# 解压（Windows 用资源管理器或 Expand-Archive）
tar -xzf ` + esc(example) + `

# 修改配置后启动
cd ` + esc(strings.TrimSuffix(example, ".tar.gz")) + `
./codeporter-agent -config agent.yaml</code></pre>
<p class="desc" style="margin-top:16px">
  完整校验清单见 <a href="./SHA256SUMS.txt">SHA256SUMS.txt</a>，
  机器可读的产物索引见 <a href="./latest.json">latest.json</a>。
</p>
<footer>
  ` + esc(projectName()) + ` · 构建于 ` + time.Now().Format("2006-01-02 15:04") + ` ·
  客户端仅发起出站连接，本机不监听端口
</footer>
</div>
<script>
(function () {
  var ua = navigator.userAgent || "";
  var os = "linux";
  if (/Windows/i.test(ua)) { os = "windows"; }
  else if (/Mac OS X|Macintosh/i.test(ua)) { os = "darwin"; }
  else if (/Android/i.test(ua)) { os = "linux"; }

  var arch = "amd64";
  var ud = navigator.userAgentData;
  if (ud && ud.architecture) {
    if (/arm/i.test(ud.architecture)) { arch = "arm64"; }
  } else if (/aarch64|arm64/i.test(ua)) {
    arch = "arm64";
  } else if (os === "darwin" && !/Intel/i.test(ua)) {
    arch = "arm64";
  }

  var cards = document.querySelectorAll(".card");
  var hit = null;
  for (var i = 0; i < cards.length; i++) {
    var c = cards[i];
    if (c.getAttribute("data-goos") === os && c.getAttribute("data-goarch") === arch) {
      hit = c; break;
    }
  }
  if (!hit) {
    for (var j = 0; j < cards.length; j++) {
      if (cards[j].getAttribute("data-goos") === os) { hit = cards[j]; break; }
    }
  }
  if (hit) {
    hit.classList.add("recommended");
    var tag = document.createElement("span");
    tag.className = "tag";
    tag.textContent = "推荐";
    hit.appendChild(tag);
    var hint = document.getElementById("hint");
    if (hint) { hint.textContent = "已检测到你的系统为 " + os + "/" + arch + "，推荐下载已高亮。"; }
  }
})();
</script>
</body>
</html>
`
}

// projectName 返回页脚展示的项目名。
func projectName() string { return "CodePorter" }

// filterBy 按产品类型筛选产物。
func filterBy(artifacts []artifact, product string) []artifact {
	out := make([]artifact, 0, len(artifacts))
	for _, a := range artifacts {
		if a.Product == product {
			out = append(out, a)
		}
	}
	return out
}

// esc 转义 HTML 文本节点。
func esc(s string) string { return html.EscapeString(s) }

// attr 转义 HTML 属性值。
func attr(s string) string {
	r := strings.NewReplacer(`"`, "&#34;", `'`, "&#39;", "<", "&lt;", ">", "&gt;", "&", "&amp;")
	return r.Replace(s)
}

// writeTextFile 写入文本文件。
func writeTextFile(path, body string) error {
	return os.WriteFile(path, []byte(body), 0o644)
}
