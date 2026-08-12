package handlers

import (
	"bytes"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"github.com/helloxz/zacp/internal/acp/providers"
	"github.com/helloxz/zacp/internal/service"
)

// 配置文件读写错误哨兵（映射见 writeConfigFileError）。
var (
	// errAgentNotFound 智能体未登记配置文件映射。
	errAgentNotFound = errors.New("agent has no config files")
	// errConfigFileNotInMap 请求的路径不在该智能体的配置白名单内。
	errConfigFileNotInMap = errors.New("config file not in map")
)

// configFileContentResponse 配置文件内容（path 为 ~/... 相对形式，不泄露绝对路径；
// mtimeUnixMs 供前端保存时回传做乐观锁比对）。
type configFileContentResponse struct {
	Path        string `json:"path"`
	Name        string `json:"name"`
	Content     string `json:"content"`
	Size        int64  `json:"size"`
	MtimeUnixMs int64  `json:"mtimeUnixMs"`
}

// ListConfigFiles GET /api/v1/agents/:agentId/config-files
//
// 返回该智能体「真实存在」的配置文件列表（按登记顺序）。
// 路径以 ~/... 相对形式下发（前端展示与回传都用该形式，后端展开 + 白名单校验，
// 避免在接口中暴露本机绝对路径）。文件不存在 / 无权限 stat 的条目直接跳过，不阻塞其余文件。
func (h *AgentManageHandler) ListConfigFiles(c *gin.Context) {
	agentID := c.Param("agentId")
	paths, ok := providers.ConfigFilePaths(agentID)
	if !ok {
		writeError(c, http.StatusNotFound, "agent_not_found", "智能体不存在或未登记配置文件")
		return
	}

	files := make([]gin.H, 0, len(paths))
	for _, p := range paths {
		abs, err := providers.ExpandHomePath(p)
		if err != nil {
			// HOME 解析失败（极少数环境）时跳过该文件
			continue
		}
		info, err := os.Stat(abs)
		if err != nil || info.IsDir() {
			continue
		}
		files = append(files, gin.H{
			"path": p,
			"name": filepath.Base(abs),
			"ext":  strings.TrimPrefix(strings.ToLower(filepath.Ext(abs)), "."),
		})
	}
	c.JSON(http.StatusOK, gin.H{"files": files})
}

// ReadConfigFileContent GET /api/v1/agents/:agentId/config-files/content?path=...
//
// 读取单个配置文件内容。校验链：白名单路径 → 是文件 → ≤2MB → 非二进制（无 NUL）→ 合法 UTF-8，
// 与工作区文本编辑器（service.ReadTextFile）保持一致，保证「能打开就能保存」。
func (h *AgentManageHandler) ReadConfigFileContent(c *gin.Context) {
	abs, rel, err := resolveConfigFilePath(c.Param("agentId"), c.Query("path"))
	if err != nil {
		writeConfigFileError(c, err)
		return
	}

	info, err := os.Stat(abs)
	if err != nil {
		writeConfigFileError(c, err)
		return
	}
	if info.IsDir() {
		writeError(c, http.StatusBadRequest, "not_directory", "路径是目录")
		return
	}
	if info.Size() > service.MaxEditableSizeBytes {
		writeError(c, http.StatusRequestEntityTooLarge, "file_too_large", "文件超过 2MB，不支持文本编辑")
		return
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		writeConfigFileError(c, err)
		return
	}
	if bytes.IndexByte(data, 0) >= 0 {
		writeError(c, http.StatusUnsupportedMediaType, "binary_file", "二进制文件不支持文本编辑")
		return
	}
	if !utf8.Valid(data) {
		writeError(c, http.StatusUnsupportedMediaType, "invalid_encoding", "文件不是合法 UTF-8 编码，不支持编辑")
		return
	}

	c.JSON(http.StatusOK, configFileContentResponse{
		Path:        rel,
		Name:        filepath.Base(abs),
		Content:     string(data),
		Size:        info.Size(),
		MtimeUnixMs: info.ModTime().UnixMilli(),
	})
}

// writeConfigFileContentRequest 配置文件保存请求体。
type writeConfigFileContentRequest struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	// ExpectedMtime 打开时记录的 mtime（毫秒）；可选，携带时后端做乐观锁比对。
	ExpectedMtime *int64 `json:"expectedMtime,omitempty"`
}

// WriteConfigFileContent PUT /api/v1/agents/:agentId/config-files/content
//
// 保存配置文件。校验链：白名单路径 → 内容 ≤2MB / 无 NUL / 合法 UTF-8 →
// 格式语法校验（json/yaml/toml，.env 跳过）→（可选）mtime 乐观锁。
// 语法错误返回 400（invalid_syntax）禁止保存，防止损坏配置写盘；
// 乐观锁冲突返回 409（file_modified），前端据此引导重新加载。
func (h *AgentManageHandler) WriteConfigFileContent(c *gin.Context) {
	var req writeConfigFileContentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	abs, _, err := resolveConfigFilePath(c.Param("agentId"), req.Path)
	if err != nil {
		writeConfigFileError(c, err)
		return
	}
	info, err := os.Stat(abs)
	if err != nil {
		writeConfigFileError(c, err)
		return
	}
	if info.IsDir() {
		writeError(c, http.StatusBadRequest, "not_directory", "路径是目录")
		return
	}
	// 与读侧镜像校验：大小、NUL、UTF-8（UTF-8 校验放在 NUL 之后，NUL 本身是合法 UTF-8）
	if int64(len(req.Content)) > service.MaxEditableSizeBytes {
		writeError(c, http.StatusRequestEntityTooLarge, "file_too_large", "文件超过 2MB，不支持文本编辑")
		return
	}
	if bytes.IndexByte([]byte(req.Content), 0) >= 0 {
		writeError(c, http.StatusUnsupportedMediaType, "binary_file", "内容含二进制字节，拒绝保存")
		return
	}
	if !utf8.ValidString(req.Content) {
		writeError(c, http.StatusUnsupportedMediaType, "invalid_encoding", "内容不是合法 UTF-8 编码，拒绝保存")
		return
	}
	// 格式语法校验（按扩展名；.env 等无格式文件跳过）
	if err := providers.ValidateConfigSyntax(req.Path, []byte(req.Content)); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_syntax", err.Error())
		return
	}
	// mtime 乐观锁：仅在客户端显式回传期望值时校验（老客户端不带则不强制）
	if req.ExpectedMtime != nil && info.ModTime().UnixMilli() != *req.ExpectedMtime {
		writeError(c, http.StatusConflict, "file_modified", "文件已被其他端修改，请重新加载后再保存")
		return
	}

	// 已存在文件的权限位保持不变（os.WriteFile 不改变现有权限）
	if err := os.WriteFile(abs, []byte(req.Content), 0o644); err != nil {
		writeConfigFileError(c, err)
		return
	}
	newInfo, err := os.Stat(abs)
	if err != nil {
		writeConfigFileError(c, err)
		return
	}
	c.JSON(http.StatusOK, configFileContentResponse{
		Path:        req.Path,
		Name:        filepath.Base(abs),
		Content:     req.Content,
		Size:        newInfo.Size(),
		MtimeUnixMs: newInfo.ModTime().UnixMilli(),
	})
}

// resolveConfigFilePath 校验 agentId 与 path（~/... 形式）均命中内置映射，返回展开后的绝对路径。
// 白名单校验防路径穿越：path 必须是该智能体登记列表中的原样值，不接受任何拼接/变形。
func resolveConfigFilePath(agentID, rel string) (abs string, original string, err error) {
	paths, ok := providers.ConfigFilePaths(agentID)
	if !ok {
		return "", "", errAgentNotFound
	}
	for _, p := range paths {
		if p == rel {
			a, err := providers.ExpandHomePath(p)
			if err != nil {
				return "", "", err
			}
			return a, p, nil
		}
	}
	return "", "", errConfigFileNotInMap
}

// writeConfigFileError 输出配置文件读写统一错误结构。
// 2MB / 二进制 / UTF-8 / 乐观锁 / 权限等错误复用 writeFileError 的既有映射。
func writeConfigFileError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, errAgentNotFound):
		writeError(c, http.StatusNotFound, "agent_not_found", "智能体不存在或未登记配置文件")
	case errors.Is(err, errConfigFileNotInMap):
		writeError(c, http.StatusBadRequest, "config_file_not_found", "配置文件不在该智能体的配置列表中")
	case errors.Is(err, os.ErrNotExist):
		writeError(c, http.StatusNotFound, "path_not_found", "配置文件不存在")
	default:
		writeFileError(c, err)
	}
}
