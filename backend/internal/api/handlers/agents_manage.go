package handlers

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/helloxz/zacp/internal/acp/manager"
	"github.com/helloxz/zacp/internal/acp/providers"
	"github.com/helloxz/zacp/internal/config"
)

// AgentManageHandler 设置页「智能体」管理接口：
//   - GET /api/v1/agents/manage — 全量列表（配置 + 内置，含 installed/enabled）
//   - PUT /api/v1/agents/:agentId — 开启/关闭（写 config.toml + 运行时热更新）
//
// 注意与 GET /api/v1/agents（运行时可用 agent 列表）语义区分：
// 后者只含 enabled 配置项，供新建会话选择器使用；本 handler 面向设置页展示与开关。
type AgentManageHandler struct {
	Mgr        *manager.Manager
	ConfigPath string // config.toml 绝对路径（$ZACP_DATA/config.toml 或 ZACP_CONFIG）
}

// manageAgentResponse 设置页列表单条数据（对应 providers.CatalogItem 的 JSON 结构）。
type manageAgentResponse struct {
	AgentID   string `json:"agentId"`
	Name      string `json:"name"`
	Command   string `json:"command"`
	Enabled   bool   `json:"enabled"`
	Installed bool   `json:"installed"`
	Source    string `json:"source"` // "config" | "builtin"
}

// ListManageAgents 返回设置页智能体全量列表。
// 每次从配置文件现读 [[agents]]（而非启动时缓存），保证开关写回后列表立即一致；
// 配置文件缺失/解析失败时降级为仅内置列表，不阻塞设置页展示。
func (h *AgentManageHandler) ListManageAgents(c *gin.Context) {
	configured, err := config.ReadAgents(h.ConfigPath)
	if err != nil {
		// 解析失败时降级为仅内置列表（设置页仍可展示），错误交给统一错误处理中间件
		_ = c.Error(err)
		configured = nil
	}

	items := providers.BuildCatalog(configured)
	resp := make([]manageAgentResponse, 0, len(items))
	for _, it := range items {
		resp = append(resp, manageAgentResponse{
			AgentID:   it.AgentID,
			Name:      it.Name,
			Command:   it.Command,
			Enabled:   it.Enabled,
			Installed: it.Installed,
			Source:    it.Source,
		})
	}
	c.JSON(http.StatusOK, gin.H{"agents": resp})
}

type setAgentEnabledRequest struct {
	Enabled *bool `json:"enabled" binding:"required"`
}

// SetAgentEnabled 开启/关闭指定智能体：
//  1. 校验目标存在（catalog 内）且开启时已安装
//  2. 写回 config.toml（存在则更新 enabled 行，不存在则追加 [[agents]] 块）
//  3. 运行时热更新 registry（开启立即可用、关闭停进程）
func (h *AgentManageHandler) SetAgentEnabled(c *gin.Context) {
	agentID := c.Param("agentId")

	var req setAgentEnabledRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Enabled == nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{"code": "bad_request", "message": "body requires {\"enabled\": true|false}"},
		})
		return
	}
	enabled := *req.Enabled

	// 1. 校验目标存在 + 安装状态（catalog 实时从文件构建）
	configured, err := config.ReadAgents(h.ConfigPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{"code": "read_config", "message": "failed to read config: " + err.Error()},
		})
		return
	}
	var target *providers.CatalogItem
	items := providers.BuildCatalog(configured)
	for i := range items {
		it := &items[i]
		if strings.EqualFold(it.AgentID, agentID) {
			target = it
			break
		}
	}
	if target == nil {
		c.JSON(http.StatusNotFound, gin.H{
			"error": gin.H{"code": "agent_not_found", "message": "agent '" + agentID + "' not found"},
		})
		return
	}
	if enabled && !target.Installed {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{"code": "agent_not_installed", "message": "agent '" + agentID + "' is not installed on this machine, install it first"},
		})
		return
	}

	// 构造写回模板：优先文件中的配置项（保留用户自定义 command/args），否则用内置模板
	var tpl config.AgentConfig
	found := false
	for _, a := range configured {
		if strings.EqualFold(a.ID, agentID) {
			tpl = a
			found = true
			break
		}
	}
	if !found {
		tpl, found = providers.BuiltinTemplate(agentID)
		if !found {
			c.JSON(http.StatusNotFound, gin.H{
				"error": gin.H{"code": "agent_not_found", "message": "agent '" + agentID + "' has no template"},
			})
			return
		}
	}
	tpl.Enabled = enabled

	// 2. 写回配置文件（失败则整体中止，不改运行时）
	if err := config.SetAgentEnabled(h.ConfigPath, tpl, enabled); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{"code": "write_config", "message": "failed to update config: " + err.Error()},
		})
		return
	}

	// 3. 运行时热更新（配置已落盘；热更新失败不影响持久化结果，前端刷新可见）
	if err := h.Mgr.SetAgentEnabled(tpl, enabled); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{"code": "hot_update_failed", "message": "config saved but runtime update failed: " + err.Error()},
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"ok": true, "enabled": enabled})
}
