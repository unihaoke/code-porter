// Command release 构建 CodePorter 的可下载发布产物。
//
// 它完成三件事：
//  1. 交叉编译 agent（本地客户端）与 gateway（服务端）到多个 GOOS/GOARCH；
//  2. 把「二进制 + 示例配置 + 快速上手说明」打包为 zip（Windows）或 tar.gz（其他平台）；
//  3. 生成 SHA256SUMS.txt、latest.json 与一个可直接托管的 index.html 下载页。
//
// 用法：
//
//	go run ./scripts/release -version v0.2.0            # 全部平台，输出到 dist/
//	go run ./scripts/release -only agent -out ./release  # 仅客户端
//
// 之所以用 Go 实现而非 shell 脚本：归档与校验和逻辑不依赖系统是否安装 zip/tar，
// 在 Windows / macOS / Linux 上行为完全一致。
package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// modulePath 用于 -ldflags 注入版本变量的包路径。
const modulePath = "github.com/codeporter/code-porter/pkg/version"

// target 描述一个交叉编译目标。
type target struct {
	goos   string
	goarch string
	// ext 是 Windows 可执行后缀，其他平台为空。
	ext string
	// label 是面向用户的平台名称。
	label string
}

// archiveExt 决定打包格式：Windows 用 zip（资源管理器原生支持），其余用 tar.gz。
func (t target) archiveExt() string {
	if t.goos == "windows" {
		return "zip"
	}
	return "tar.gz"
}

// agentTargets 本地客户端覆盖主流开发者桌面平台。
var agentTargets = []target{
	{"windows", "amd64", ".exe", "Windows x64"},
	{"windows", "arm64", ".exe", "Windows ARM64"},
	{"darwin", "arm64", "", "macOS Apple Silicon (M1/M2/M3)"},
	{"darwin", "amd64", "", "macOS Intel"},
	{"linux", "amd64", "", "Linux x64"},
	{"linux", "arm64", "", "Linux ARM64"},
}

// gatewayTargets 网关服务部署目标（服务器场景）。
var gatewayTargets = []target{
	{"linux", "amd64", "", "Linux x64"},
	{"linux", "arm64", "", "Linux ARM64"},
	{"darwin", "arm64", "", "macOS Apple Silicon"},
}

// artifact 描述一个已生成的发布产物。
type artifact struct {
	Product string `json:"product"`
	File    string `json:"file"`
	Label   string `json:"label"`
	GOOS    string `json:"goos"`
	GOARCH  string `json:"goarch"`
	Size    int64  `json:"size"`
	SizeMB  string `json:"size_human"`
	SHA256  string `json:"sha256"`
}

// archiveFile 是待写入归档的单个文件。
type archiveFile struct {
	name string
	body []byte
	mode os.FileMode
}

func main() {
	relVersion := flag.String("version", "v0.2.0", "release version tag, e.g. v0.2.0")
	outDir := flag.String("out", "dist", "output directory")
	only := flag.String("only", "all", "what to build: agent | gateway | all")
	project := flag.String("project", "CodePorter", "project name shown on the download page")
	flag.Parse()

	build := strings.TrimSpace(strings.ToLower(*only))
	if build != "agent" && build != "gateway" && build != "all" {
		fatalf("invalid -only value %q, want agent|gateway|all", *only)
	}

	if err := run(*outDir, *relVersion, *project, build); err != nil {
		fatalf("%v", err)
	}
}

func run(outDir, relVersion, project, build string) error {
	start := time.Now()
	commit := gitOutput("rev-parse", "--short", "HEAD")
	if commit == "" {
		commit = "unknown"
	}
	buildDate := time.Now().UTC().Format(time.RFC3339)

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("create out dir: %w", err)
	}
	stage, err := os.MkdirTemp(outDir, ".stage-")
	if err != nil {
		return fmt.Errorf("create stage dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(stage) }()

	ldflags := fmt.Sprintf("-s -w -X %s.Version=%s -X %s.Commit=%s -X %s.BuildDate=%s",
		modulePath, relVersion, modulePath, commit, modulePath, buildDate)

	var artifacts []artifact
	plans := []struct {
		product string
		pkg     string
		targets []target
		conf    string
	}{
		{"agent", "./cmd/agent", agentTargets, "configs/agent.yaml"},
		{"gateway", "./cmd/gateway", gatewayTargets, "configs/gateway.yaml"},
	}
	for _, p := range plans {
		if build != "all" && build != p.product {
			continue
		}
		confBody, err := os.ReadFile(p.conf)
		if err != nil {
			return fmt.Errorf("read %s: %w", p.conf, err)
		}
		for _, t := range p.targets {
			a, err := buildOne(stage, outDir, p.product, p.pkg, relVersion, ldflags, confBody, t)
			if err != nil {
				return err
			}
			artifacts = append(artifacts, a)
			fmt.Printf("  built %s (%s)\n", a.File, a.SizeMB)
		}
	}
	sort.Slice(artifacts, func(i, j int) bool {
		if artifacts[i].Product != artifacts[j].Product {
			return artifacts[i].Product < artifacts[j].Product
		}
		if artifacts[i].GOOS != artifacts[j].GOOS {
			return artifacts[i].GOOS < artifacts[j].GOOS
		}
		return artifacts[i].GOARCH < artifacts[j].GOARCH
	})

	if err := writeChecksums(filepath.Join(outDir, "SHA256SUMS.txt"), artifacts); err != nil {
		return err
	}
	if err := writeManifest(filepath.Join(outDir, "latest.json"), relVersion, commit, buildDate, artifacts); err != nil {
		return err
	}
	if err := writeIndexHTML(filepath.Join(outDir, "index.html"), project, relVersion, commit, buildDate, artifacts); err != nil {
		return err
	}

	fmt.Printf("\nrelease %s ready in %s (%d artifacts, %s)\n",
		relVersion, outDir, len(artifacts), time.Since(start).Round(time.Millisecond))
	return nil
}

// buildOne 编译单个目标并打包为归档。
func buildOne(stage, outDir, product, pkg, relVersion, ldflags string, confBody []byte, t target) (artifact, error) {
	binName := "codeporter-" + product + t.ext
	binPath := filepath.Join(stage, product+"-"+t.goos+"-"+t.goarch+binName)
	if err := goBuild(pkg, binPath, ldflags, t); err != nil {
		return artifact{}, err
	}
	binBody, err := os.ReadFile(binPath)
	if err != nil {
		return artifact{}, fmt.Errorf("read built binary: %w", err)
	}

	root := fmt.Sprintf("codeporter-%s-%s-%s-%s", product, relVersion, t.goos, t.goarch)
	archiveName := root + "." + t.archiveExt()
	files := []archiveFile{
		{name: binName, body: binBody, mode: 0o755},
		{name: product + ".yaml", body: confBody, mode: 0o644},
		{name: "README.md", body: []byte(readmeFor(product, t, relVersion, binName, archiveName)), mode: 0o644},
	}
	archivePath := filepath.Join(outDir, archiveName)

	if t.goos == "windows" {
		err = writeZip(archivePath, root, files)
	} else {
		err = writeTarGz(archivePath, root, files)
	}
	if err != nil {
		return artifact{}, err
	}

	sum, size, err := hashFile(archivePath)
	if err != nil {
		return artifact{}, err
	}
	return artifact{
		Product: product,
		File:    archiveName,
		Label:   t.label,
		GOOS:    t.goos,
		GOARCH:  t.goarch,
		Size:    size,
		SizeMB:  humanSize(size),
		SHA256:  sum,
	}, nil
}

// goBuild 以静态链接方式交叉编译一个包。
func goBuild(pkg, out, ldflags string, t target) error {
	cmd := exec.Command("go", "build", "-trimpath", "-ldflags", ldflags, "-o", out, pkg)
	cmd.Env = append(os.Environ(),
		"CGO_ENABLED=0",
		"GOOS="+t.goos,
		"GOARCH="+t.goarch,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go build %s for %s/%s: %w", pkg, t.goos, t.goarch, err)
	}
	return nil
}

// --- 归档 ---

func writeZip(dst, root string, files []archiveFile) error {
	f, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("create zip: %w", err)
	}
	defer func() { _ = f.Close() }()

	zw := zip.NewWriter(f)
	for _, af := range files {
		w, err := zw.CreateHeader(&zip.FileHeader{
			Name:   root + "/" + af.name,
			Method: zip.Deflate,
		})
		if err != nil {
			return fmt.Errorf("zip entry %s: %w", af.name, err)
		}
		if _, err := w.Write(af.body); err != nil {
			return fmt.Errorf("zip write %s: %w", af.name, err)
		}
	}
	if err := zw.Close(); err != nil {
		return fmt.Errorf("close zip: %w", err)
	}
	return f.Close()
}

func writeTarGz(dst, root string, files []archiveFile) error {
	f, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("create tar.gz: %w", err)
	}
	defer func() { _ = f.Close() }()

	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)
	for _, af := range files {
		if err := tw.WriteHeader(&tar.Header{
			Name:     root + "/" + af.name,
			Mode:     int64(af.mode),
			Size:     int64(len(af.body)),
			ModTime:  time.Now(),
			Typeflag: tar.TypeReg,
			Format:   tar.FormatGNU,
		}); err != nil {
			return fmt.Errorf("tar header %s: %w", af.name, err)
		}
		if _, err := tw.Write(af.body); err != nil {
			return fmt.Errorf("tar write %s: %w", af.name, err)
		}
	}
	if err := tw.Close(); err != nil {
		return fmt.Errorf("close tar: %w", err)
	}
	if err := gw.Close(); err != nil {
		return fmt.Errorf("close gzip: %w", err)
	}
	return f.Close()
}

// --- 辅助产物 ---

func writeChecksums(path string, artifacts []artifact) error {
	var b bytes.Buffer
	for _, a := range artifacts {
		fmt.Fprintf(&b, "%s  %s\n", a.SHA256, a.File)
	}
	if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
		return fmt.Errorf("write checksums: %w", err)
	}
	return nil
}

func writeManifest(path, relVersion, commit, buildDate string, artifacts []artifact) error {
	payload := struct {
		Version     string     `json:"version"`
		Commit      string     `json:"commit"`
		BuildDate   string     `json:"build_date"`
		GeneratedAt string     `json:"generated_at"`
		Artifacts   []artifact `json:"artifacts"`
	}{
		Version:     relVersion,
		Commit:      commit,
		BuildDate:   buildDate,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Artifacts:   artifacts,
	}
	body, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	body = append(body, '\n')
	if err := os.WriteFile(path, body, 0o644); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	return nil
}

// hashFile 计算文件的 SHA256 与字节大小。
func hashFile(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, fmt.Errorf("open for hashing: %w", err)
	}
	defer func() { _ = f.Close() }()

	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, fmt.Errorf("hash: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// gitOutput 读取 git 信息，任何失败都返回空串（版本信息不阻碍发布）。
func gitOutput(args ...string) string {
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "release: "+format+"\n", args...)
	os.Exit(1)
}
