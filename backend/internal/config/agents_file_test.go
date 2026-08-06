package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// SetAgentEnabled：更新已存在块内的 enabled 行，其余内容（含注释）原样保留。
func TestSetAgentEnabled_UpdatesExistingBlock(t *testing.T) {
	path := writeTempConfig(t, `# 用户手写注释，必须保留
[server]
addr = ":8680"

# 智能体
[[agents]]
id = "reasonix"
name = "Reasonix"
enabled = true
command = "reasonix"
args = ["--acp"]

[[agents]]
id = "grok"
name = "Grok"
enabled = true
command = "grok"
args = ["agent", "stdio"]
`)

	if err := SetAgentEnabled(path, AgentConfig{ID: "reasonix"}, false); err != nil {
		t.Fatalf("SetAgentEnabled: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	s := string(got)

	// 注释与其它段保留
	for _, want := range []string{"# 用户手写注释，必须保留", "[server]", "addr = \":8680\"", "# 智能体"} {
		if !strings.Contains(s, want) {
			t.Errorf("lost content: %q not found in:\n%s", want, s)
		}
	}
	// reasonix 被关闭，grok 不变
	if !strings.Contains(s, "id = \"reasonix\"\nname = \"Reasonix\"\nenabled = false") {
		t.Errorf("reasonix should be disabled:\n%s", s)
	}
	if !strings.Contains(s, "id = \"grok\"\nname = \"Grok\"\nenabled = true") {
		t.Errorf("grok should stay enabled:\n%s", s)
	}
}

// SetAgentEnabled：块内原本没有 enabled 行时，应在块内插入一行（而非别处）。
func TestSetAgentEnabled_InsertsEnabledLine(t *testing.T) {
	path := writeTempConfig(t, `[server]
addr = ":8680"

[[agents]]
id = "omp"
name = "Omp"
command = "omp"
args = ["acp"]
`)

	if err := SetAgentEnabled(path, AgentConfig{ID: "omp"}, true); err != nil {
		t.Fatalf("SetAgentEnabled: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	s := string(got)
	// enabled 行应插入在 omp 块内（command 行之后）
	want := "id = \"omp\"\nname = \"Omp\"\ncommand = \"omp\"\nargs = [\"acp\"]\nenabled = true"
	if !strings.Contains(s, want) {
		t.Errorf("enabled line not inserted inside block, got:\n%s", s)
	}
}

// SetAgentEnabled：配置中不存在该 id 时，应在文件尾追加完整 [[agents]] 块。
func TestSetAgentEnabled_AppendsNewBlock(t *testing.T) {
	path := writeTempConfig(t, `[server]
addr = ":8680"

[[agents]]
id = "reasonix"
name = "Reasonix"
enabled = true
command = "reasonix"
`)

	if err := SetAgentEnabled(path, AgentConfig{ID: "grok", Name: "Grok", Command: "grok", Args: []string{"agent", "stdio"}}, true); err != nil {
		t.Fatalf("SetAgentEnabled: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	s := string(got)
	if !strings.Contains(s, "[[agents]]\nid = \"grok\"\nname = \"Grok\"\nenabled = true\ncommand = \"grok\"\nargs = [\"agent\", \"stdio\"]") {
		t.Errorf("new block not appended correctly, got:\n%s", s)
	}
	// 原块不动
	if !strings.Contains(s, "id = \"reasonix\"\nname = \"Reasonix\"\nenabled = true") {
		t.Errorf("existing block modified unexpectedly:\n%s", s)
	}
}

// SetAgentEnabled：文件不存在时创建最小配置并写入 agent 块。
func TestSetAgentEnabled_CreatesMissingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	if err := SetAgentEnabled(path, AgentConfig{ID: "omp", Name: "Omp", Command: "omp", Args: []string{"acp"}}, true); err != nil {
		t.Fatalf("SetAgentEnabled: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	s := string(got)
	for _, want := range []string{"[server]", "[session]", "[database]", "id = \"omp\"", "enabled = true", "args = [\"acp\"]"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in generated config:\n%s", want, s)
		}
	}
}

// ReadAgents：解析已有配置的 [[agents]] 列表，保持顺序与字段。
func TestReadAgents(t *testing.T) {
	path := writeTempConfig(t, `[server]
addr = ":8680"

[[agents]]
id = "reasonix"
name = "Reasonix"
enabled = true
command = "reasonix"
args = ["--acp"]

# 被注释掉的 agent 不会出现
# [[agents]]
# id = "jcode"

[[agents]]
id = "grok"
name = "Grok"
enabled = true
command = "grok"
args = ["agent", "stdio"]
`)

	agents, err := ReadAgents(path)
	if err != nil {
		t.Fatalf("ReadAgents: %v", err)
	}
	if len(agents) != 2 {
		t.Fatalf("got %d agents, want 2", len(agents))
	}
	if agents[0].ID != "reasonix" || agents[0].Command != "reasonix" || len(agents[0].Args) != 1 || agents[0].Args[0] != "--acp" || !agents[0].Enabled {
		t.Errorf("agent[0] parsed wrong: %+v", agents[0])
	}
	if agents[1].ID != "grok" || len(agents[1].Args) != 2 || agents[1].Args[1] != "stdio" {
		t.Errorf("agent[1] parsed wrong: %+v", agents[1])
	}
}

// ReadAgents：文件不存在时返回空列表（不报错），供设置页降级展示内置目录。
func TestReadAgents_MissingFile(t *testing.T) {
	agents, err := ReadAgents(filepath.Join(t.TempDir(), "nope.toml"))
	if err != nil {
		t.Fatalf("ReadAgents: %v", err)
	}
	if len(agents) != 0 {
		t.Fatalf("got %d agents, want 0", len(agents))
	}
}

// ReadAgents：enabled 未书写时按 TOML 语义默认为 true。
func TestReadAgents_EnabledDefaultTrue(t *testing.T) {
	path := writeTempConfig(t, `
[[agents]]
id = "reasonix"
command = "reasonix"
`)
	agents, err := ReadAgents(path)
	if err != nil {
		t.Fatalf("ReadAgents: %v", err)
	}
	if len(agents) != 1 || !agents[0].Enabled {
		t.Fatalf("want enabled=true default, got %+v", agents)
	}
}

// SetAgentEnabled：替换 enabled 行时保留行尾 # 注释。
func TestSetAgentEnabled_KeepsTrailingComment(t *testing.T) {
	path := writeTempConfig(t, `[[agents]]
id = "reasonix"
enabled = true # 默认开启，勿动
command = "reasonix"
`)

	if err := SetAgentEnabled(path, AgentConfig{ID: "reasonix"}, false); err != nil {
		t.Fatalf("SetAgentEnabled: %v", err)
	}
	got, _ := os.ReadFile(path)
	s := string(got)
	if !strings.Contains(s, "enabled = false # 默认开启，勿动") {
		t.Errorf("trailing comment lost, got:\n%s", s)
	}
}

// SetAgentEnabled：大写 ENABLED 键也能被替换（避免产生重复键）。
func TestSetAgentEnabled_UppercaseEnabledKey(t *testing.T) {
	path := writeTempConfig(t, `[[agents]]
id = "reasonix"
ENABLED = true
command = "reasonix"
`)

	if err := SetAgentEnabled(path, AgentConfig{ID: "reasonix"}, false); err != nil {
		t.Fatalf("SetAgentEnabled: %v", err)
	}
	got, _ := os.ReadFile(path)
	s := string(got)
	// 应原地替换 ENABLED 行，而非追加第二个 enabled 键
	if strings.Contains(s, "ENABLED") {
		t.Errorf("ENABLED key should be rewritten in place, got:\n%s", s)
	}
	if strings.Count(s, "enabled") != 1 {
		t.Errorf("want exactly one enabled key, got:\n%s", s)
	}
}

// SetAgentEnabled：原文件末尾已有空行时，追加块前不会出现双空行。
func TestSetAgentEnabled_NoDoubleBlankLine(t *testing.T) {
	path := writeTempConfig(t, `[server]
addr = ":8680"

[[agents]]
id = "reasonix"
enabled = true
command = "reasonix"

`)

	if err := SetAgentEnabled(path, AgentConfig{ID: "grok", Name: "Grok", Command: "grok"}, true); err != nil {
		t.Fatalf("SetAgentEnabled: %v", err)
	}
	got, _ := os.ReadFile(path)
	s := string(got)
	if strings.Contains(s, "\n\n\n") {
		t.Errorf("double blank line before appended block, got:\n%q", s)
	}
	if !strings.HasSuffix(s, "\n") {
		t.Errorf("file should end with newline, got:\n%q", s)
	}
}
