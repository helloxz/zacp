package providers

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/helloxz/zacp/internal/pkg/tomledit"
)

// Zlite 默认渠道设置：读写 ~/.zlite/config.toml 中 name='default' 的
// [[providers]] 块 + ~/.zlite/.env 中的 ZLITE_DEFAULT_API_KEY。
//
// 与 zacp 自身配置（~/.zacp/config.toml）无关，本文件只面向 zlite 的
// 独立配置目录。行级编辑（tomledit）保证不破坏用户手工写入的其它内容
// （其它 providers 块、注释、格式），与 internal/config/agents_file.go
// 对 [[agents]] 的增量编辑模式一致；若未来出现第三个类似的「第三方配置
// 结构化编辑」需求，可考虑把该模式抽成公共实现。
//
// 写入的两个文件存在先后一致的中间态问题：单进程内用 zliteWriteMu 串行化，
// 假设与 config 包相同（单用户单进程），多进程并发写同一批文件不在支持范围。

const (
	// zliteConfigRel / zliteEnvRel 为相对 HOME 的路径（映射见 AgentConfigPaths）
	zliteConfigRel = "~/.zlite/config.toml"
	zliteEnvRel    = "~/.zlite/.env"

	// zliteEnvKey 为 .env 中默认渠道 API 密钥的固定键名
	// （config.toml 中通过 ${ZLITE_DEFAULT_API_KEY} 引用，zh 见用户约定）。
	zliteEnvKey = "ZLITE_DEFAULT_API_KEY"

	// ZliteChannelTypeOpenAIChat 等为默认渠道支持的 type（前后端一致的落盘值）
	ZliteChannelTypeOpenAIChat     = "openai.chat"
	ZliteChannelTypeOpenAIResponse = "openai.responses"
	ZliteChannelTypeAnthropic      = "anthropic"
)

// 行级写锁：config.toml 与 .env 需「读旧值 → 行级修改 → 原子写」成对完成，
// 并发保存会基于旧内容各自写回导致互相覆盖（与 configWriteMu 同理）。
var zliteWriteMu sync.Mutex

// ZliteDefaultChannel 默认渠道设置（JSON 序列化与前端 camelCase 对齐，
// 见 frontend types/models.ts 的 ZliteChannel）。
type ZliteDefaultChannel struct {
	Type    string   `json:"type"`
	BaseURL string   `json:"baseUrl"`
	APIKey  string   `json:"apiKey"`
	Models  []string `json:"models"`
}

// ValidZliteChannelType 判断渠道类型是否合法（与前端下拉选项一致）。
func ValidZliteChannelType(t string) bool {
	switch t {
	case ZliteChannelTypeOpenAIChat, ZliteChannelTypeOpenAIResponse, ZliteChannelTypeAnthropic:
		return true
	}
	return false
}

// ValidateZliteChannel 校验保存入参：
//   - type 必须是三个合法值之一
//   - base_url 必填且以 http:// 或 https:// 开头，不含单引号/换行
//   - models 必填（至少一个有效模型），元素去重保序、剔空，且不含单引号/换行
//     （TOML 字面量限制）
//   - apiKey 允许为空（本地模型场景），非空时同样剔除前后空白、不含换行
//
// 返回规范化后的副本；调用方应使用返回值落盘（去重/trim 结果以本函数为准）。
func ValidateZliteChannel(ch ZliteDefaultChannel) (ZliteDefaultChannel, error) {
	if !ValidZliteChannelType(ch.Type) {
		return ch, fmt.Errorf("type 必须是 openai.chat / openai.responses / anthropic 之一")
	}
	baseURL := strings.TrimSpace(ch.BaseURL)
	if baseURL == "" {
		return ch, fmt.Errorf("base_url 不能为空")
	}
	if !strings.HasPrefix(baseURL, "http://") && !strings.HasPrefix(baseURL, "https://") {
		return ch, fmt.Errorf("base_url 必须以 http:// 或 https:// 开头")
	}
	if strings.ContainsAny(baseURL, "'\n") {
		return ch, fmt.Errorf("base_url 不能包含单引号或换行")
	}

	seen := make(map[string]bool, len(ch.Models))
	models := make([]string, 0, len(ch.Models))
	for _, m := range ch.Models {
		m = strings.TrimSpace(m)
		if m == "" || seen[m] {
			continue // 空条目剔除、重复去重
		}
		if strings.ContainsAny(m, "'\n") {
			return ch, fmt.Errorf("模型名不能包含单引号或换行")
		}
		seen[m] = true
		models = append(models, m)
	}
	// 可用模型必填：不允许留空（与前端表单校验保持一致）
	if len(models) == 0 {
		return ch, fmt.Errorf("models 不能为空，至少需要一个可用模型")
	}

	apiKey := strings.TrimSpace(ch.APIKey)
	if strings.Contains(apiKey, "\n") {
		return ch, fmt.Errorf("api_key 不能包含换行")
	}

	ch.BaseURL = baseURL
	ch.APIKey = apiKey
	ch.Models = models
	return ch, nil
}

// ReadZliteDefaultChannel 读取默认渠道设置：
//   - config.toml 不存在或无 name='default' 块：Type 返回默认 openai.chat，
//     其余字段零值（前端表单可直接使用）
//   - .env 不存在或无密钥行：APIKey 回退读取 config.toml 中 default 块的
//     api_key 字段——若它是 ${ZLITE_DEFAULT_API_KEY} 一类环境变量引用（无实际
//     密钥内容）则仍返回空串，若为明文则以明文兜底展示（兼容手工配置场景）
//
// 解析一律尽力而为：个别字段解析失败不阻塞整体返回（保留其它字段）。
func ReadZliteDefaultChannel() (ZliteDefaultChannel, error) {
	ch := ZliteDefaultChannel{Type: ZliteChannelTypeOpenAIChat}

	cfgPath, err := ExpandHomePath(zliteConfigRel)
	if err != nil {
		return ch, err
	}
	var cfgAPIKey string // config.toml 中 default 块的 api_key 原文（做兜底候选）
	if raw, err := os.ReadFile(cfgPath); err == nil {
		parsed, key := parseDefaultChannel(string(raw))
		ch.Type = parsed.Type
		ch.BaseURL = parsed.BaseURL
		ch.Models = parsed.Models
		cfgAPIKey = key
	} else if !os.IsNotExist(err) {
		return ch, fmt.Errorf("read %s: %w", zliteConfigRel, err)
	}

	// api_key 以 .env 为准（config.toml 里的 ${ZLITE_DEFAULT_API_KEY} 只是引用）
	envPath, err := ExpandHomePath(zliteEnvRel)
	if err != nil {
		return ch, err
	}
	if raw, err := os.ReadFile(envPath); err == nil {
		ch.APIKey = readEnvKey(splitLines(string(raw)))
	} else if !os.IsNotExist(err) {
		return ch, fmt.Errorf("read %s: %w", zliteEnvRel, err)
	}

	// .env 未提供密钥时，以 config.toml 的明文 api_key 兜底（仅当它不是
	// ${ZLITE_DEFAULT_API_KEY} 这类环境变量引用——引用无实际密钥内容）。
	if ch.APIKey == "" && cfgAPIKey != "" && !strings.Contains(cfgAPIKey, "${ZLITE_DEFAULT_API_KEY}") {
		ch.APIKey = cfgAPIKey
	}

	// 历史遗留：config.toml 可能写过非法的 type（手改/旧版本），保持原值返回，
	// 前端下拉不匹配时显示原值，用户重新选择合法值保存即可。
	return ch, nil
}

// parseDefaultChannel 解析 config.toml 文本中 name='default' 的 [[providers]] 块，
// 返回其字段与 api_key 原文（未匹配到 default 块时返回 Type 默认值）。
// 独立成函数便于单元测试（不依赖真实 HOME 目录）。
func parseDefaultChannel(content string) (ch ZliteDefaultChannel, cfgAPIKey string) {
	ch.Type = ZliteChannelTypeOpenAIChat
	lines := splitLines(content)
	for _, b := range tomledit.ParseArrayBlocks(lines, "providers") {
		if !blockHasDefaultName(lines, b) {
			continue
		}
		for i := b.Start + 1; i < b.End; i++ {
			key, raw, ok := tomledit.KeyValue(lines[i])
			if !ok {
				continue
			}
			switch strings.ToLower(key) {
			case "type":
				if v := tomledit.StringValue(raw); v != "" {
					ch.Type = v
				}
			case "base_url":
				ch.BaseURL = tomledit.StringValue(raw)
			case "api_key":
				cfgAPIKey = tomledit.StringValue(raw)
			case "models":
				ch.Models = tomledit.ListValue(raw)
			}
		}
		return ch, cfgAPIKey
	}
	return ch, cfgAPIKey
}

// SaveZliteDefaultChannel 保存默认渠道设置：
//  1. 规范化输入（trim/去重）并校验——非法输入直接返回错误，不落盘
//  2. 行级更新 .env 的 ZLITE_DEFAULT_API_KEY（空值 = 删除该键行，其余行保留）
//  3. 行级更新 config.toml 的 name='default' [[providers]] 块
//     （不存在则该块，不存在则文件尾追加；api_key 恒为 ${ZLITE_DEFAULT_API_KEY}）
//  4. models 为空时省略 models 行（继承 zlite 自身默认模型）
//
// 两个文件都写成功才算成功；任一失败返回错误（已写的那个文件保持新内容，
// 单用户场景可接受，错误信息会指明失败点）。
func SaveZliteDefaultChannel(ch ZliteDefaultChannel) error {
	valid, err := ValidateZliteChannel(ch)
	if err != nil {
		return err
	}

	zliteWriteMu.Lock()
	defer zliteWriteMu.Unlock()

	envPath, err := ExpandHomePath(zliteEnvRel)
	if err != nil {
		return err
	}
	cfgPath, err := ExpandHomePath(zliteConfigRel)
	if err != nil {
		return err
	}
	// 两个文件都可能首次创建：先确保 ~/.zlite 目录存在（权限 0700，含密钥文件）
	if err := os.MkdirAll(filepath.Dir(envPath), 0700); err != nil {
		return fmt.Errorf("create ~/.zlite: %w", err)
	}

	// 2. 写 .env
	if err := writeZliteEnvFile(envPath, valid.APIKey); err != nil {
		return err
	}

	// 3. 写 config.toml
	return writeZliteConfigFile(cfgPath, valid)
}

// ---------------------------------------------------------------------------
// 内部实现
// ---------------------------------------------------------------------------

// blockHasDefaultName 判断 providers 块内是否声明 name = 'default'（大小写不敏感值比较）。
func blockHasDefaultName(lines []string, b tomledit.Block) bool {
	for i := b.Start + 1; i < b.End; i++ {
		key, raw, ok := tomledit.KeyValue(lines[i])
		if ok && strings.EqualFold(key, "name") && strings.EqualFold(tomledit.StringValue(raw), "default") {
			return true
		}
	}
	return false
}

// envKeyRe 匹配 .env 中目标密钥行（允许 `export ` 前缀与键后空白，键名精确大写）。
var envKeyRe = regexp.MustCompile(`^(?:export\s+)?` + regexp.QuoteMeta(zliteEnvKey) + `\s*=`)

// readEnvKey 读取 .env 中目标密钥值（剥引号、剥行尾注释；未命中返回空串）。
func readEnvKey(lines []string) string {
	for _, l := range lines {
		if !envKeyRe.MatchString(l) {
			continue
		}
		idx := strings.Index(l, "=")
		v := strings.TrimSpace(l[idx+1:])
		if i := strings.Index(v, " #"); i >= 0 {
			v = strings.TrimSpace(v[:i]) // 行尾注释
		}
		if len(v) >= 2 && ((v[0] == '\'' && v[len(v)-1] == '\'') || (v[0] == '"' && v[len(v)-1] == '"')) {
			v = v[1 : len(v)-1]
		}
		return v
	}
	return ""
}

// writeZliteEnvFile 行级更新 .env：value 非空 → 替换/追加目标键行；
// value 为空 → 删除目标键行（其余行原样保留）。文件不存在时新建。
func writeZliteEnvFile(path, value string) error {
	raw, err := os.ReadFile(path)
	var lines []string
	if err == nil {
		lines = splitLines(string(raw))
	} else if os.IsNotExist(err) {
		lines = nil
	} else {
		return fmt.Errorf("read %s: %w", zliteEnvRel, err)
	}
	eol := tomledit.EOL(string(raw))

	if value == "" {
		// 删除该键行（保留其它行）
		out := lines[:0]
		for _, l := range lines {
			if !envKeyRe.MatchString(l) {
				out = append(out, l)
			}
		}
		lines = out
	} else {
		replaced := false
		for i, l := range lines {
			if envKeyRe.MatchString(l) {
				lines[i] = zliteEnvKey + "=" + value
				replaced = true
				break
			}
		}
		if !replaced {
			lines = append(lines, zliteEnvKey+"="+value)
		}
	}

	content := strings.Join(lines, "\n")
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	return tomledit.WriteFileAtomic(path, tomledit.RestoreEOL(content, eol))
}

// zliteDefaultBlockLines 生成默认渠道 [[providers]] 块的字段行（固定顺序，
// 与用户约定的示例一致）。models 已由校验保证非空（防御：若历史调用方传入空
// 列表则省略 models 行，继承 zlite 自身默认模型）。
func zliteDefaultBlockLines(ch ZliteDefaultChannel) []string {
	lines := []string{
		"api_key = '${ZLITE_DEFAULT_API_KEY}'", // 固定引用：实际值在 .env 的 ZLITE_DEFAULT_API_KEY
		"base_url = " + tomlLiteral(ch.BaseURL),
	}
	if len(ch.Models) > 0 {
		quoted := make([]string, len(ch.Models))
		for i, m := range ch.Models {
			quoted[i] = tomlLiteral(m)
		}
		lines = append(lines, "models = ["+strings.Join(quoted, ", ")+"]")
	}
	lines = append(lines,
		"name = 'default'", // 固定：设置页只管理默认渠道
		"type = "+tomlLiteral(ch.Type),
	)
	return lines
}

// writeZliteConfigFile 行级更新 config.toml 的默认渠道块：
//   - 已存在 name='default' 块：逐键更新（models 为空时删除 models 行）
//   - 不存在：文件尾追加新块
//   - 文件不存在：新建最小配置（仅默认渠道块）
func writeZliteConfigFile(path string, ch ZliteDefaultChannel) error {
	var lines []string
	eol := "\n"
	if b, err := os.ReadFile(path); err == nil {
		lines = splitLines(string(b))
		eol = tomledit.EOL(string(b))
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read %s: %w", zliteConfigRel, err)
	} else {
		lines = nil
	}

	var defaultBlock *tomledit.Block
	blocks := tomledit.ParseArrayBlocks(lines, "providers")
	for i := range blocks {
		if blockHasDefaultName(lines, blocks[i]) {
			defaultBlock = &blocks[i]
			break
		}
	}

	if defaultBlock == nil {
		// 无 default 块：文件尾追加（含文件不存在的新建场景）
		lines = tomledit.AppendArrayBlock(lines, "providers", zliteDefaultBlockLines(ch))
	} else {
		// 已有 default 块：整块替换为固定字段顺序（api_key/base_url/models/name/type），
		// 块内旧注释一并清除；models 为空时省略 models 行（继承 zlite 默认）。
		// 整体替换比逐键更新更稳：避免多次插入导致的行序混乱，且字段顺序固定。
		replacement := append([]string{"[[providers]]"}, zliteDefaultBlockLines(ch)...)
		if defaultBlock.End < len(lines) {
			// 块后还有内容：补一个空行分隔（原块尾空行随替换被移除）
			replacement = append(replacement, "")
		}
		lines = tomledit.ReplaceArrayBlock(lines, *defaultBlock, replacement)
	}
	content := strings.Join(lines, "\n")
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	return tomledit.WriteFileAtomic(path, tomledit.RestoreEOL(content, eol))
}

// tomlLiteral 把字符串序列化为 TOML 字面量：首选单引号 literal string
// （与用户示例一致）；值内含单引号/换行时回退双引号转义字符串
// （strconv.Quote 可表达任意内容，防御非法输入）。
func tomlLiteral(s string) string {
	if !strings.ContainsAny(s, "'\n") {
		return "'" + s + "'"
	}
	return strconv.Quote(s)
}

// splitLines 按 \n 切分行（CRLF 由 EOL 检测 + RestoreEOL 在写回时还原）。
func splitLines(s string) []string {
	return strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
}
