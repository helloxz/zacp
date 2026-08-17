package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// 本文件实现 config.toml 中 [[agents]] 段的增量读写：
// 设置页开关智能体时只更新/追加 [[agents]] 数组元素，文件其余内容
// （server/session/database 等段落、用户手写注释）原样保留，避免
// Viper 全量序列化导致注释丢失。

// agentBlock 表示文件中一个 [[agents]] 数组元素块。
// start 为块起始行号（含 [[agents]] 行），end 为块结束行号（不含，
// 指向下一个 section 起始行或文件行数）。
type agentBlock struct {
	cfg   AgentConfig
	start int
	end   int
}

var (
	// agentsSectionRe 匹配 [[agents]] 数组元素起始行
	agentsSectionRe = regexp.MustCompile(`^\[\[agents\]\]\s*$`)
	// sectionRe 匹配其它 [table] / [[array]] 段起始行（注意不会匹配 [[agents]]）
	sectionRe = regexp.MustCompile(`^\[[^\]]*\]\s*$`)
	// keyValueRe 匹配 TOML 键值行，如 `  id = "reasonix"`
	keyValueRe = regexp.MustCompile(`^\s*([A-Za-z0-9_]+)\s*=\s*(.*?)\s*$`)
	// enabledKeyRe 匹配块内的 enabled 键（不区分大小写，与 parseAgentKeys 的小写归一化对齐；
	// 否则用户写 ENABLED 时无法命中替换，会插入重复键导致 config.Load 失败）
	enabledKeyRe = regexp.MustCompile(`(?i)^\s*enabled\s*=`)
)

// configWriteMu 串行化 config.toml 的所有写回（agents 开关与 auth 凭证两个设置入口）。
// 两个写回都是「读旧值 → 行级修改 → 原子写」，若不互斥，并发保存会基于旧内容各自写回、
// 后写者静默覆盖前写者的修改。单用户场景并发概率极低，但一把互斥锁的成本可忽略。
var configWriteMu sync.Mutex

// ReadAgents 从配置文件解析 [[agents]] 列表（保持书写顺序）。
// 文件不存在时返回空列表；解析失败返回错误。
func ReadAgents(configPath string) ([]AgentConfig, error) {
	content, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read config: %w", err)
	}
	blocks := parseAgentBlocks(strings.Split(string(content), "\n"))
	agents := make([]AgentConfig, 0, len(blocks))
	for _, b := range blocks {
		agents = append(agents, b.cfg)
	}
	return agents, nil
}

// SetAgentEnabled 将指定 id 的智能体 enabled 置为指定值并原子写回：
//   - 配置中已存在该 id：仅更新其块内的 enabled 行（保留其它字段、行尾注释与行尾风格）
//   - 配置中不存在：文件末尾追加一个 [[agents]] 块（用 agent 模板的完整字段）
//   - 配置文件不存在：创建最小配置并写入
//
// agent 的其它字段（command/args 等）在「不存在时追加」场景下作为模板使用。
// 注意：本函数只做增量行级编辑，不重新序列化整个文件，用户手写注释得以保留。
func SetAgentEnabled(configPath string, agent AgentConfig, enabled bool) error {
	configWriteMu.Lock()
	defer configWriteMu.Unlock()
	agent.Enabled = enabled

	raw, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return writeMinimalConfig(configPath, agent)
		}
		return fmt.Errorf("read config: %w", err)
	}

	// 行尾风格检测：Windows 下用户可能用 CRLF，写回时保持一致（避免混合行尾）
	eol := "\n"
	if strings.Contains(string(raw), "\r\n") {
		eol = "\r\n"
	}
	// 统一归一化为 LF 便于逐行处理（CRLF 的 \r 会干扰行匹配）
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	blocks := parseAgentBlocks(lines)

	// 已有块：只改 enabled 行（id 大小写不敏感匹配，与 catalog 去重规则一致）
	for _, b := range blocks {
		if strings.EqualFold(b.cfg.ID, agent.ID) {
			out := strings.Join(setEnabledInBlock(lines, b, enabled), "\n")
			return writeFileAtomic(configPath, restoreEOL(out, eol))
		}
	}

	// 未找到：文件尾追加新块。去掉尾部空白后统一补：块前一个空行、文件以换行结尾。
	out := strings.TrimRight(strings.Join(lines, "\n"), "\n")
	out += "\n\n" + strings.Join(blockLines(agent), "\n") + "\n"
	return writeFileAtomic(configPath, restoreEOL(out, eol))
}

// RemoveAgent 从配置文件删除指定 id 的 [[agents]] 块并原子写回：
//   - 配置中存在该 id：删除其块（含块内所有字段行），块后紧跟的分隔空行一并删除，
//     避免删除后悬挂多余空行
//   - 配置中不存在：返回错误（正常流程调用方已先 ReadAgents 校验过，此处兜底）
//
// 与 SetAgentEnabled 共用 configWriteMu 互斥锁，保证并发写回不互相覆盖；
// 文件行尾风格（CRLF）与权限保持。删除不可恢复（config.toml 无备份），
// 由调用方（前端确认弹窗）负责确认。
func RemoveAgent(configPath, agentID string) error {
	configWriteMu.Lock()
	defer configWriteMu.Unlock()

	raw, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("agent '%s' not found in config", agentID)
		}
		return fmt.Errorf("read config: %w", err)
	}
	eol := "\n"
	if strings.Contains(string(raw), "\r\n") {
		eol = "\r\n"
	}
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	blocks := parseAgentBlocks(lines)

	for _, b := range blocks {
		if strings.EqualFold(b.cfg.ID, agentID) {
			// 块后紧跟的连续空行随块删除（通常是块与下一段的分隔空行）；
			// 块前的空行/注释保留（可能归属上一段，无法可靠区分归属，保守保留）
			end := b.end
			for end < len(lines) && strings.TrimSpace(lines[end]) == "" {
				end++
			}
			out := append(append([]string{}, lines[:b.start]...), lines[end:]...)
			joined := strings.TrimRight(strings.Join(out, "\n"), "\n") + "\n"
			return writeFileAtomic(configPath, restoreEOL(joined, eol))
		}
	}
	return fmt.Errorf("agent '%s' not found in config", agentID)
}

// restoreEOL 将 LF 内容按目标行尾风格还原（eol 为 "\n" 时原样返回）。
func restoreEOL(s, eol string) string {
	if eol == "\n" {
		return s
	}
	return strings.ReplaceAll(s, "\n", eol)
}

// parseAgentBlocks 扫描行序列，切分出所有 [[agents]] 块并解析块内键值。
func parseAgentBlocks(lines []string) []agentBlock {
	var blocks []agentBlock
	start := -1
	flush := func(end int) {
		if start < 0 {
			return
		}
		blocks = append(blocks, agentBlock{
			cfg:   parseAgentKeys(lines[start+1 : end]),
			start: start,
			end:   end,
		})
		start = -1
	}
	for i, line := range lines {
		switch {
		case agentsSectionRe.MatchString(line):
			flush(i)
			start = i
		case sectionRe.MatchString(line):
			flush(i)
		}
	}
	flush(len(lines))
	return blocks
}

// parseAgentKeys 解析块内若干行 TOML 键值，得到 AgentConfig。
// 无法识别的行（注释等）忽略；数组/字符串解析失败时该字段保持零值（尽力而为）。
func parseAgentKeys(blockLines []string) AgentConfig {
	cfg := AgentConfig{Enabled: true} // TOML 未写 enabled 时默认 true（与 bool 零值相反，需显式置初值）
	for _, line := range blockLines {
		m := keyValueRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		key, raw := m[1], strings.TrimSpace(m[2])
		// TOML 键大小写敏感但实际配置可能写 ID/Enabled 等大写形式，
		// 统一按小写识别（与 viper 解析行为对齐）。
		switch strings.ToLower(key) {
		case "id":
			cfg.ID = parseString(raw)
		case "name":
			cfg.Name = parseString(raw)
		case "enabled":
			if v, err := strconv.ParseBool(raw); err == nil {
				cfg.Enabled = v
			}
		case "transport":
			cfg.Transport = parseString(raw)
		case "command":
			cfg.Command = parseString(raw)
		case "cwd":
			cfg.Cwd = parseString(raw)
		case "args":
			cfg.Args = parseStringList(raw)
		case "env":
			cfg.Env = parseStringList(raw)
		}
	}
	return cfg
}

// setEnabledInBlock 在指定块内更新 enabled 行；块内无 enabled 行时在块尾插入。
// 替换时保留行首缩进与行尾 # 注释（enabled 值为布尔，行内不会出现 #）。
func setEnabledInBlock(lines []string, b agentBlock, enabled bool) []string {
	// 块内已有 enabled 键：替换该行的值，保留缩进与行尾注释
	for i := b.start; i < b.end; i++ {
		if enabledKeyRe.MatchString(lines[i]) {
			indent := leadingWhitespace(lines[i])
			comment := ""
			if idx := strings.Index(lines[i], "#"); idx >= 0 {
				comment = strings.TrimSpace(lines[i][idx:])
			}
			lines[i] = indent + "enabled = " + strconv.FormatBool(enabled)
			if comment != "" {
				lines[i] += " " + comment
			}
			return lines
		}
	}
	// 无 enabled 键：在块内最后一个非空行后插入（跳过块尾可能存在的空行）
	insertAt := b.end
	for insertAt > b.start+1 && strings.TrimSpace(lines[insertAt-1]) == "" {
		insertAt--
	}
	// 缩进与块内属性行一致（默认两个空格）
	indent := "  "
	for i := b.start + 1; i < b.end; i++ {
		if strings.TrimSpace(lines[i]) != "" {
			indent = leadingWhitespace(lines[i])
			break
		}
	}
	line := indent + "enabled = " + strconv.FormatBool(enabled)
	lines = append(lines[:insertAt], append([]string{line}, lines[insertAt:]...)...)
	return lines
}

// blockLines 生成一个完整的 [[agents]] 块文本行（有值的字段才写出）。
func blockLines(a AgentConfig) []string {
	b := []string{"[[agents]]", "id = " + quoteString(a.ID)}
	if a.Name != "" {
		b = append(b, "name = "+quoteString(a.Name))
	}
	b = append(b, "enabled = "+strconv.FormatBool(a.Enabled))
	if a.Transport != "" {
		b = append(b, "transport = "+quoteString(a.Transport))
	}
	if a.Command != "" {
		b = append(b, "command = "+quoteString(a.Command))
	}
	if len(a.Args) > 0 {
		b = append(b, "args = "+stringListLiteral(a.Args))
	}
	if a.Cwd != "" {
		b = append(b, "cwd = "+quoteString(a.Cwd))
	}
	if len(a.Env) > 0 {
		b = append(b, "env = "+stringListLiteral(a.Env))
	}
	return b
}

// writeMinimalConfig 配置文件不存在时生成最小配置（server/session/database + 目标 agent 块）。
// 说明：这里的默认值（:8680/debug/data/zacp.db）可能与启动时 --addr/--config 覆盖不一致；
// 该分支仅在 config.toml 缺失时触发（正常启动流程会自动创建），属罕见兜底，接受此取舍。
func writeMinimalConfig(configPath string, agent AgentConfig) error {
	var sb strings.Builder
	sb.WriteString("# zacp 运行时配置（由设置页首次开启智能体时自动生成）\n")
	sb.WriteString("[server]\naddr = \":8680\"\nmode = \"debug\"\n\n")
	sb.WriteString("[session]\ndefault_cwd = \".\"\nauto_approve = false\nidle_timeout = \"30m\"\n\n")
	sb.WriteString("[database]\npath = \"data/zacp.db\"\n\n")
	sb.WriteString(strings.Join(blockLines(agent), "\n") + "\n")
	return writeFileAtomic(configPath, sb.String())
}

// writeFileAtomic 原子写文件：同目录临时文件 + rename，保留原文件权限。
func writeFileAtomic(path, content string) error {
	mode := os.FileMode(0600)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".zacp-config-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp config: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		// rename 成功后 tmpName 已不存在，os.Remove 返回错误可忽略
		_ = os.Remove(tmpName)
	}()

	if _, err := tmp.WriteString(content); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp config: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temp config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp config: %w", err)
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return fmt.Errorf("chmod temp config: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename config: %w", err)
	}
	return nil
}

// parseString 解析 TOML 字符串字面量（双引号支持转义；单引号为字面量字符串）。
func parseString(raw string) string {
	raw = strings.TrimSpace(raw)
	if len(raw) < 2 {
		return ""
	}
	if raw[0] == '"' && raw[len(raw)-1] == '"' {
		if s, err := strconv.Unquote(raw); err == nil {
			return s
		}
		return raw[1 : len(raw)-1]
	}
	if raw[0] == '\'' && raw[len(raw)-1] == '\'' {
		return raw[1 : len(raw)-1]
	}
	return ""
}

// parseStringList 解析 TOML 字符串数组字面量，如 ["agent", "stdio"]。
// 元素间逗号分隔；引号串用 parseString 解析（支持单/双引号）。
func parseStringList(raw string) []string {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "[") || !strings.HasSuffix(raw, "]") {
		return nil
	}
	inner := strings.TrimSpace(raw[1 : len(raw)-1])
	if inner == "" {
		return nil
	}
	parts := strings.Split(inner, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := parseString(strings.TrimSpace(p)); s != "" {
			result = append(result, s)
		}
	}
	return result
}

// quoteString 用 TOML 基本字符串形式引用（与 Go strconv.Quote 转义规则基本兼容）。
func quoteString(s string) string {
	return strconv.Quote(s)
}

// stringListLiteral 生成 TOML 字符串数组字面量，如 ["agent", "stdio"]。
func stringListLiteral(items []string) string {
	quoted := make([]string, len(items))
	for i, it := range items {
		quoted[i] = quoteString(it)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

// leadingWhitespace 返回行首空白（用于保持缩进风格）。
func leadingWhitespace(line string) string {
	i := 0
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	return line[:i]
}
