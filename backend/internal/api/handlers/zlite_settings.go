package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/helloxz/zacp/internal/acp/providers"
)

// zlite 官方智能体的「默认渠道设置」结构化读写接口：
//   - GET /api/v1/agents/zlite/default-channel — 读取
//   - PUT /api/v1/agents/zlite/default-channel — 保存
//
// 与通用全文件编辑接口（config-files/content）互补：本接口面向
// ~/.zlite/config.toml 的 name='default' [[providers]] 块与 ~/.zlite/.env
// 的 ZLITE_DEFAULT_API_KEY 做表单级读写，路径固定（不接收用户传 path，
// 由 providers 包内 ExpandHomePath 展开，白名单精神与 config-files 一致）。
// 仅在 zlite 已安装时前端显示入口，本接口不重复校验安装状态（保存失败会
// 以文件错误形式返回）。

// GetZliteDefaultChannel 读取默认渠道设置（GET）。
// 文件不存在/未配置时返回默认值（type=openai.chat，其余为空），前端可直接回填表单。
func (h *AgentManageHandler) GetZliteDefaultChannel(c *gin.Context) {
	ch, err := providers.ReadZliteDefaultChannel()
	if err != nil {
		writeError(c, http.StatusInternalServerError, "read_zlite_channel", err.Error())
		return
	}
	c.JSON(http.StatusOK, ch)
}

// zliteChannelRequest PUT 请求体（json 字段 camelCase，与 ZliteDefaultChannel 对齐）。
type zliteChannelRequest struct {
	Type    string   `json:"type" binding:"required"`
	BaseURL string   `json:"baseUrl" binding:"required"`
	APIKey  string   `json:"apiKey"`
	Models  []string `json:"models"`
}

// SaveZliteDefaultChannel 保存默认渠道设置（PUT）：
//  1. 校验入参（type 枚举 / base_url 格式 / models 去重，由 providers.ValidateZliteChannel
//     负责，前后端保持同一套规则），失败返回 400 invalid_zlite_channel
//  2. 行级写回 ~/.zlite/config.toml 与 ~/.zlite/.env（失败返回 500 write_zlite_channel）
func (h *AgentManageHandler) SaveZliteDefaultChannel(c *gin.Context) {
	var req zliteChannelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{"code": "bad_request", "message": "body requires {\"type\", \"baseUrl\", \"apiKey\", \"models\"}"},
		})
		return
	}
	ch := providers.ZliteDefaultChannel{
		Type:    req.Type,
		BaseURL: req.BaseURL,
		APIKey:  req.APIKey,
		Models:  req.Models,
	}
	if _, err := providers.ValidateZliteChannel(ch); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{"code": "invalid_zlite_channel", "message": err.Error()},
		})
		return
	}
	if err := providers.SaveZliteDefaultChannel(ch); err != nil {
		writeError(c, http.StatusInternalServerError, "write_zlite_channel", err.Error())
		return
	}
	// 渠道配置已落盘：停止运行中的 zlite ACP 进程，让其下次按需启动时
	// 读取最新配置（StopAgent 只停进程/会话连接，不动 zacp 自身 config.toml、
	// 不清除 registry 注册，下次新建会话时后端会自动按新配置拉起）。
	// 停止失败不影响已保存的配置，但需要明确告知（语义同现有 hot_update_failed）。
	if err := h.Mgr.StopAgent("zlite"); err != nil {
		writeError(c, http.StatusInternalServerError, "hot_update_failed",
			"config saved but stopping zlite agent failed: "+err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
