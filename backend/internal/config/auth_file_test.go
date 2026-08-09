package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func readConfig(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestSetAuthCredentialsAddsNewSection(t *testing.T) {
	path := writeTempConfig(t, "[server]\naddr = \":8680\"\n")
	if err := SetAuthCredentials(path, "admin", "sha256$abc"); err != nil {
		t.Fatal(err)
	}
	got := readConfig(t, path)
	if !strings.Contains(got, "[auth]") {
		t.Fatalf("应追加 [auth] 段:\n%s", got)
	}
	if !strings.Contains(got, `username = "admin"`) || !strings.Contains(got, `password_hash = "sha256$abc"`) {
		t.Fatalf("应写入 username/password_hash:\n%s", got)
	}
	if !strings.Contains(got, `addr = ":8680"`) {
		t.Fatalf("原有内容应保留:\n%s", got)
	}
}

func TestSetAuthCredentialsUpdatesInPlace(t *testing.T) {
	// 已有 [auth] 段 + 段内注释：原位更新两键，注释与其它键保留
	path := writeTempConfig(t, `[auth]
# 用户注释
username = "old"
password_hash = "sha256$old"
[server]
addr = ":8680"
`)
	if err := SetAuthCredentials(path, "newuser", "sha256$new"); err != nil {
		t.Fatal(err)
	}
	got := readConfig(t, path)
	if !strings.Contains(got, "# 用户注释") {
		t.Fatalf("段内注释应保留:\n%s", got)
	}
	if !strings.Contains(got, `username = "newuser"`) || !strings.Contains(got, `password_hash = "sha256$new"`) {
		t.Fatalf("应原位更新两个键:\n%s", got)
	}
	// 更新后不应残留旧值
	if strings.Contains(got, "old") {
		t.Fatalf("旧凭证应被替换:\n%s", got)
	}
	// 后续段落不受影响
	if !strings.Contains(got, `addr = ":8680"`) {
		t.Fatalf("其它段应保留:\n%s", got)
	}
}

func TestSetAuthCredentialsClearsPassword(t *testing.T) {
	path := writeTempConfig(t, "[auth]\nusername = \"admin\"\npassword_hash = \"sha256$x\"\n")
	if err := SetAuthCredentials(path, "admin", ""); err != nil {
		t.Fatal(err)
	}
	got := readConfig(t, path)
	if !strings.Contains(got, `password_hash = ""`) {
		t.Fatalf("密码留空应写空哈希（关闭认证）:\n%s", got)
	}
}

func TestSetAuthCredentialsCreatesMinimalConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml") // 文件不存在
	if err := SetAuthCredentials(path, "admin", "sha256$abc"); err != nil {
		t.Fatal(err)
	}
	got := readConfig(t, path)
	for _, want := range []string{"[server]", "[session]", "[database]", "[auth]", `username = "admin"`, `password_hash = "sha256$abc"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("最小配置应包含 %q:\n%s", want, got)
		}
	}
}

func TestSetAuthCredentialsPreservesCRLF(t *testing.T) {
	path := writeTempConfig(t, "[server]\r\naddr = \":8680\"\r\n")
	if err := SetAuthCredentials(path, "admin", "sha256$x"); err != nil {
		t.Fatal(err)
	}
	got := readConfig(t, path)
	if strings.Contains(strings.ReplaceAll(got, "\r\n", ""), "\n") {
		// 归一化后不应再有裸 \n（即原文全是 \r\n）
		if strings.Count(got, "\r\n") != strings.Count(got, "\n") {
			t.Fatalf("CRLF 文件写回后应保持 CRLF:\n%q", got)
		}
	}
}

func TestSetAuthCredentialsAfterAgentBlock(t *testing.T) {
	// 回归：[[agents]] 数组块在文件中时，findSection 的段边界不能把 agents 段误判为 [auth] 的结束
	path := writeTempConfig(t, "[server]\naddr = \":8680\"\n\n[[agents]]\nid = \"reasonix\"\ncommand = \"rz\"\n")
	if err := SetAuthCredentials(path, "admin", "sha256$x"); err != nil {
		t.Fatal(err)
	}
	got := readConfig(t, path)
	if !strings.Contains(got, "id = \"reasonix\"") || !strings.Contains(got, "command = \"rz\"") {
		t.Fatalf("[[agents]] 块应原样保留:\n%s", got)
	}
	if !strings.Contains(got, `password_hash = "sha256$x"`) {
		t.Fatalf("应追加 [auth] 段:\n%s", got)
	}
}
