// Package tomledit 提供 TOML 文件的轻量「行级」块编辑工具。
//
// 动机：设置页对第三方工具的配置文件（如 ~/.zlite/config.toml）做结构化读写时，
// 若用 TOML 库整体解析再序列化，会丢失原文件的注释与书写格式。本包按行扫描、
// 只定位并修改目标 [table] / [[array]] 块，其余行原样保留（参照
// internal/config/agents_file.go 对 [[agents]] 的增量编辑模式）。
//
// 适用边界：面向「结构简单、键为平面 key = value」的 TOML 文件；嵌套 table
// 请用完整 TOML 解析库。
package tomledit

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Block 表示一个 [table] / [[array]] 段在行序列中的位置。
// Start 为段起始行（含 [..] / [[..]] 行），End 为段结束行（不含，
// 指向下一个段起始行或文件行数）。
type Block struct {
	Start int
	End   int
}

// sectionRe 匹配任意段起始行（`[xxx]` / `[[xxx]]`），用于截断块边界。
// 与 config 包约定一致：`[^\]]*` 使形如 `[[xxx]]` 的行不会被本正则匹配，
// 数组块边界由 ParseArrayBlocks 的目标 section 判断，其余段由本正则负责。
var sectionRe = regexp.MustCompile(`^\[[^\]]*\]\s*$`)

// keyValueRe 匹配块内平面键值行，如 `  type = 'openai.chat'`。
var keyValueRe = regexp.MustCompile(`^\s*([A-Za-z0-9_]+)\s*=\s*(.*?)\s*$`)

// ParseArrayBlocks 扫描行序列，切分出所有 [[section]] 数组元素块。
// 块边界由下一个段起始行（任意 [table] / [[array]]）截断。
func ParseArrayBlocks(lines []string, section string) []Block {
	target := "[[" + section + "]]"
	var blocks []Block
	start := -1
	flush := func(end int) {
		if start < 0 {
			return
		}
		blocks = append(blocks, Block{Start: start, End: end})
		start = -1
	}
	for i, line := range lines {
		switch {
		case strings.TrimSpace(line) == target:
			flush(i)
			start = i
		case sectionRe.MatchString(line):
			flush(i)
		}
	}
	flush(len(lines))
	return blocks
}

// KeyValue 解析一行 TOML 键值，返回键与原始值文本（未去引号）。
// 非键值行（注释、段起始、空行等）返回 ok=false。
func KeyValue(line string) (key, raw string, ok bool) {
	m := keyValueRe.FindStringSubmatch(line)
	if m == nil {
		return "", "", false
	}
	return m[1], strings.TrimSpace(m[2]), true
}

// SetKeyInBlock 在块内设置 `key = value` 行：
//   - 已存在该键：整行替换（保留行首缩进；行尾注释一并丢弃——
//     本工具写的值均为固定格式引用串，保留旧注释反而可能引发 '#' 歧义）
//   - 不存在：在块内最后一个非空行后插入（缩进与块内属性行一致，默认两个空格）
//
// value 应为合法的 TOML 值文本（如 `'openai.chat'` 或 `['a', 'b']`）。
// 返回修改后的行序列；block 指向的 Start 不变，End 可能后移。
func SetKeyInBlock(lines []string, block Block, key, value string) []string {
	key = strings.ToLower(key) // TOML 键大小写敏感，但实际文件可能写大写形式，统一小写识别
	line := key + " = " + value

	// 已存在：替换该行（缩进取原行首空白）
	for i := block.Start + 1; i < block.End; i++ {
		if k, _, ok := KeyValue(lines[i]); ok && strings.ToLower(k) == key {
			lines[i] = leadingWhitespace(lines[i]) + line
			return lines
		}
	}

	// 不存在：在块内最后一个非空行后插入（跳过块尾可能存在的空行）
	insertAt := block.End
	for insertAt > block.Start+1 && strings.TrimSpace(lines[insertAt-1]) == "" {
		insertAt--
	}
	indent := "  "
	for i := block.Start + 1; i < block.End; i++ {
		if strings.TrimSpace(lines[i]) != "" {
			indent = leadingWhitespace(lines[i])
			break
		}
	}
	lines = append(lines[:insertAt], append([]string{indent + line}, lines[insertAt:]...)...)
	return lines
}

// StringValue 提取 TOML 单/双引号字符串字面量的值。
// raw 允许带行尾注释（`# ...`）——闭引号之后的内容天然被忽略，例如
// `'default' # 默认渠道` 返回 `default`。不处理多行字符串。
func StringValue(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	q := raw[0]
	if q != '\'' && q != '"' {
		return ""
	}
	for i := 1; i < len(raw); i++ {
		if raw[i] == q {
			if q == '"' && raw[i-1] == '\\' {
				continue // 双引号内 \" 转义不闭合
			}
			return raw[1:i]
		}
	}
	return ""
}

// ListValue 提取 TOML 字符串数组字面量的元素列表（如 `['mimo-a', 'mimo-b']`）。
// 仅识别单/双引号字符串元素，其它类型忽略；raw 允许带行尾注释，
// 以最后一个 `]` 截断数组内容。面向单行数组，多行数组不支持。
func ListValue(raw string) []string {
	raw = strings.TrimSpace(raw)
	if len(raw) < 2 || raw[0] != '[' {
		return nil
	}
	end := strings.LastIndex(raw, "]")
	if end < 0 {
		return nil
	}
	var out []string
	for _, part := range strings.Split(raw[1:end], ",") {
		if s := StringValue(part); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// ReplaceArrayBlock 将指定数组块整体替换为新的块文本（blockLines 第一行
// 应为段起始行，如 [[providers]]），原块的字段行与注释全部被替换。
// 返回修改后的行序列；行数变化不影响 Start 之外的行。
func ReplaceArrayBlock(lines []string, block Block, blockLines []string) []string {
	head := append([]string{}, lines[:block.Start]...)
	tail := lines[block.End:]
	return append(append(head, blockLines...), tail...)
}

// AppendArrayBlock 在文件尾部追加一个 [[section]] 数组元素块（块前补一个空行分隔）。
func AppendArrayBlock(lines []string, section string, blockLines []string) []string {
	head := strings.Join(lines, "\n")
	head = strings.TrimRight(head, "\n")
	if head != "" {
		head += "\n\n"
	}
	body := "[[" + section + "]]\n" + strings.Join(blockLines, "\n") + "\n"
	return strings.Split(head+body, "\n")
}

// EOL 检测文本的行尾风格：包含 CRLF 时返回 "\r\n"，否则 "\n"。
func EOL(raw string) string {
	if strings.Contains(raw, "\r\n") {
		return "\r\n"
	}
	return "\n"
}

// RestoreEOL 将 LF 内容按目标行尾风格还原（eol 为 "\n" 时原样返回）。
func RestoreEOL(s, eol string) string {
	if eol == "\n" {
		return s
	}
	return strings.ReplaceAll(s, "\n", eol)
}

// WriteFileAtomic 原子写文件：同目录临时文件 + rename，保留原文件权限。
// 写临时文件时同步落盘（Sync），避免断电/崩溃后目标文件残缺。
func WriteFileAtomic(path, content string) error {
	mode := os.FileMode(0600)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tomledit-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		_ = os.Remove(tmpName) // rename 成功后已不存在，错误可忽略
	}()

	if _, err := tmp.WriteString(content); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename temp file: %w", err)
	}
	return nil
}

// leadingWhitespace 返回行首空白（用于保持缩进风格）。
func leadingWhitespace(line string) string {
	i := 0
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	return line[:i]
}
