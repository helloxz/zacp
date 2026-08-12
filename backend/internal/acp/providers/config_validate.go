package providers

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"go.yaml.in/yaml/v3"
)

// ValidateConfigSyntax 保存前按扩展名校验配置文件格式（json / yaml / toml），
// .env 等无格式文件跳过。语法不合法时返回错误（保存被拒绝），
// 避免把损坏的配置写盘导致智能体无法启动。
//
// 说明：JSON 空内容非法（会报错），TOML/YAML 空内容合法——与各格式解析器行为一致。
func ValidateConfigSyntax(path string, content []byte) error {
	ext := strings.ToLower(filepath.Ext(path))
	var err error
	switch ext {
	case ".json":
		var v any
		err = json.Unmarshal(content, &v)
	case ".yml", ".yaml":
		var v any
		err = yaml.Unmarshal(content, &v)
	case ".toml":
		var v any
		err = toml.Unmarshal(content, &v)
	default:
		return nil
	}
	if err != nil {
		return fmt.Errorf("%s 语法错误: %w", strings.ToUpper(strings.TrimPrefix(ext, ".")), err)
	}
	return nil
}
