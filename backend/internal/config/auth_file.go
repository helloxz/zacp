package config

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// 本文件实现 config.toml 中 [auth] 段的增量读写（设置页「用户设置」写回用）。
// 与 [[agents]] 写回（agents_file.go）同一套策略：行级编辑 + 原子 rename，
// 保留文件其余内容与用户手写注释，避免 Viper 全量序列化丢失注释。

// authSectionRe 匹配 [auth] 段起始行（table 形式，非数组）。
var authSectionRe = regexp.MustCompile(`^\[auth\]\s*$`)

// SetAuthCredentials 写回 [auth] 段（username / password_hash 两个键）：
//   - 文件不存在：创建最小配置并写入；
//   - 已有 [auth] 段：原位更新两个键（保留块内注释等其它行）；
//   - 无该段：文件末尾追加。
//
// username/passwordHash 同时为空 = 写出空段（等价关闭认证，与「缺省禁用」语义一致）。
func SetAuthCredentials(configPath, username, passwordHash string) error {
	configWriteMu.Lock()
	defer configWriteMu.Unlock()

	raw, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return writeMinimalAuthConfig(configPath, username, passwordHash)
		}
		return fmt.Errorf("read config: %w", err)
	}

	// 行尾风格检测：CRLF 文件写回时保持一致（避免混合行尾）
	eol := "\n"
	if strings.Contains(string(raw), "\r\n") {
		eol = "\r\n"
	}
	// 统一归一化为 LF 便于逐行处理
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")

	start, end := findSection(lines, authSectionRe)
	if start < 0 {
		// 无 [auth] 段：文件尾追加（去掉尾部空白，统一补前导空行与末尾换行）
		out := strings.TrimRight(strings.Join(lines, "\n"), "\n")
		out += "\n\n" + strings.Join(authBlockLines(username, passwordHash), "\n") + "\n"
		return writeFileAtomic(configPath, restoreEOL(out, eol))
	}
	// 原位替换：保留段首行，仅更新两个键，[auth] 段之后的其它段/内容原样保留
	kept := lines[start+1 : end]
	newBlock := upsertAuthKeys(kept, username, passwordHash)
	out := make([]string, 0, len(lines))
	out = append(out, lines[start])
	out = append(out, newBlock...)
	out = append(out, lines[end:]...)
	return writeFileAtomic(configPath, restoreEOL(strings.Join(out, "\n"), eol))
}

// findSection 在行序列中查找目标 table 段的位置。
// 返回 (段起始行, 段结束行)；结束行为下一个任意 section 起始行或 len(lines)。
// 未找到时返回 (-1, len(lines))。
func findSection(lines []string, target *regexp.Regexp) (int, int) {
	for i, line := range lines {
		if target.MatchString(line) {
			end := len(lines)
			for j := i + 1; j < len(lines); j++ {
				if sectionRe.MatchString(lines[j]) || agentsSectionRe.MatchString(lines[j]) {
					end = j
					break
				}
			}
			return i, end
		}
	}
	return -1, len(lines)
}

// upsertAuthKeys 在 [auth] 块内更新 username / password_hash 两个键：
//   - 已存在：整行替换（字符串值可能含 #，不保留行尾注释，避免注释误判）；
//   - 不存在：在块尾最后一个非空行后追加（缩进沿用块内已有键，默认两个空格）。
func upsertAuthKeys(blockLines []string, username, passwordHash string) []string {
	lines := append([]string{}, blockLines...)
	indent := "  "
	for _, line := range blockLines {
		if strings.TrimSpace(line) != "" {
			indent = leadingWhitespace(line)
			break
		}
	}
	for _, kv := range []struct{ key, value string }{
		{"username", quoteString(username)},
		{"password_hash", quoteString(passwordHash)},
	} {
		replaced := false
		for i, line := range lines {
			m := keyValueRe.FindStringSubmatch(line)
			if m != nil && strings.EqualFold(m[1], kv.key) {
				lines[i] = indent + kv.key + " = " + kv.value
				replaced = true
				break
			}
		}
		if replaced {
			continue
		}
		insertAt := len(lines)
		for insertAt > 0 && strings.TrimSpace(lines[insertAt-1]) == "" {
			insertAt--
		}
		lines = append(lines[:insertAt], append([]string{indent + kv.key + " = " + kv.value}, lines[insertAt:]...)...)
	}
	return lines
}

// authBlockLines 生成 [auth] 段文本行（含段首行）。
func authBlockLines(username, passwordHash string) []string {
	return []string{
		"[auth]",
		"# 单用户账号认证（设置页「用户设置」写回；password_hash 为空 = 无需登录）",
		"username = " + quoteString(username),
		"password_hash = " + quoteString(passwordHash),
	}
}

// writeMinimalAuthConfig 配置文件不存在时生成最小配置（server/session/database + [auth] 段）。
// 仅在 config.toml 缺失时触发（正常启动流程会自动创建），属罕见兜底分支。
func writeMinimalAuthConfig(configPath, username, passwordHash string) error {
	var sb strings.Builder
	sb.WriteString("# zacp 运行时配置（由设置页首次保存用户设置时自动生成）\n")
	sb.WriteString("[server]\naddr = \":8680\"\nmode = \"debug\"\n\n")
	sb.WriteString("[session]\ndefault_cwd = \".\"\nauto_approve = false\nidle_timeout = \"30m\"\n\n")
	sb.WriteString("[database]\npath = \"data/zacp.db\"\n\n")
	sb.WriteString(strings.Join(authBlockLines(username, passwordHash), "\n") + "\n")
	return writeFileAtomic(configPath, sb.String())
}
