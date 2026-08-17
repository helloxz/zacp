package handlers

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/helloxz/zacp/internal/acp/manager"
	"github.com/helloxz/zacp/internal/acp/providers"
	"github.com/helloxz/zacp/internal/config"
)

// AgentManageHandler 设置页「智能体」管理接口：
//   - GET /api/v1/agents/manage — 全量列表（配置 + 内置，含 installed/enabled）
//   - POST /api/v1/agents — 添加自定义智能体（写 config.toml + 热更新，默认启用）
//   - PUT /api/v1/agents/:agentId — 开启/关闭（写 config.toml + 运行时热更新）
//   - DELETE /api/v1/agents/:agentId — 删除自定义智能体（移除 config.toml + 热更新停用）
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
	// HasConfigFiles 后端是否登记了该智能体的配置文件路径（前端据此显示「编辑配置」按钮）。
	HasConfigFiles bool `json:"hasConfigFiles"`
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
			AgentID:        it.AgentID,
			Name:           it.Name,
			Command:        it.Command,
			Enabled:        it.Enabled,
			Installed:      it.Installed,
			Source:         it.Source,
			HasConfigFiles: it.HasConfigFiles,
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

// DeleteAgent 删除指定智能体（仅配置来源，内置项无配置块不可删）：
//  1. 从配置中读取目标（未找到：若为内置智能体返回 400 agent_builtin_not_deletable，
//     否则返回 404 agent_not_found）
//  2. 从 config.toml 移除该 [[agents]] 块（不可恢复）
//  3. 运行时热更新停用（registry 移除 + 停进程，幂等）
//
// 与 SetAgentEnabled 相同：配置先落盘，热更新失败返回 500 提示
// 「配置已删除但运行时更新失败」（残留的运行时注册重启后自动消除）。
// 历史会话数据保留在数据库，不受删除影响。
func (h *AgentManageHandler) DeleteAgent(c *gin.Context) {
	agentID := c.Param("agentId")

	configured, err := config.ReadAgents(h.ConfigPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{"code": "read_config", "message": "failed to read config: " + err.Error()},
		})
		return
	}
	var target *config.AgentConfig
	for i := range configured {
		if strings.EqualFold(configured[i].ID, agentID) {
			target = &configured[i]
			break
		}
	}
	if target == nil {
		// 内置智能体（未写入配置）无块可删，语义上「不可删除」而非「不存在」：
		// 单独返回明确错误码，避免与真正不存在的 id 混淆（前端内置项按钮虽已禁用，
		// 但直接调用 API 的场景后端必须自行兜底）
		if _, builtin := providers.BuiltinTemplate(agentID); builtin {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": gin.H{"code": "agent_builtin_not_deletable", "message": "agent '" + agentID + "' is a built-in agent and cannot be deleted"},
			})
			return
		}
		c.JSON(http.StatusNotFound, gin.H{
			"error": gin.H{"code": "agent_not_found", "message": "agent '" + agentID + "' not found in config"},
		})
		return
	}

	// 2. 从配置文件移除（先落盘，再热更新；失败整体中止不改运行时）
	if err := config.RemoveAgent(h.ConfigPath, agentID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{"code": "write_config", "message": "failed to remove config: " + err.Error()},
		})
		return
	}

	// 3. 热更新停用（配置已移除；未启用的 agent 调用幂等无害）
	if err := h.Mgr.SetAgentEnabled(*target, false); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{"code": "hot_update_failed", "message": "config removed but runtime update failed: " + err.Error()},
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"ok": true, "agentId": agentID})
}
// 限制字符集防止 ID 与内置目录混淆、避免歧义字符进入写盘/日志（ID 会作为
// config.toml 的 id 字段、运行时 registry 键与日志标签使用）。
var agentIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

// addAgentRequest 添加自定义智能体的请求体。
// Args 为原始参数字符串（如 `--model "gpt-4o" --acp`），由后端按引号感知规则
// 拆分为 args 数组后写入配置——解析逻辑只维护一份（后端），前端不做重复实现。
type addAgentRequest struct {
	Name    string `json:"name"`
	ID      string `json:"id"`
	Command string `json:"command"`
	Args    string `json:"args"`
}

// AddAgent 添加自定义智能体到设置页并默认启用：
//  1. 校验：name/id/command 必填、id 字符集合法
//  2. 校验 id 全局唯一：与已配置 [[agents]]、内置目录均大小写不敏感去重
//     （与 BuildCatalog/setEnabledInBlock 的去重规则一致，避免添加后覆盖内置项）
//  3. 校验 command 真实存在：路径形式查文件，命令名形式查 PATH（复用 IsInstalled）
//  4. 写回 config.toml（追加 [[agents]] 块，enabled=true）
//  5. 运行时热更新注册，添加后立即可在新建会话中选择
//
// 错误码：bad_request / agent_id_invalid / agent_id_exists /
// agent_command_not_found / read_config / write_config / hot_update_failed。
func (h *AgentManageHandler) AddAgent(c *gin.Context) {
	var req addAgentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{"code": "bad_request", "message": "body requires {\"name\", \"id\", \"command\", \"args\"}"},
		})
		return
	}
	name := strings.TrimSpace(req.Name)
	id := strings.TrimSpace(req.ID)
	command := strings.TrimSpace(req.Command)
	if name == "" || id == "" || command == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{"code": "bad_request", "message": "name, id and command are required"},
		})
		return
	}
	if !agentIDRe.MatchString(id) {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{"code": "agent_id_invalid", "message": "agent id must match ^[A-Za-z0-9][A-Za-z0-9_-]*$"},
		})
		return
	}

	// 2. 全局唯一：配置中的 [[agents]] 优先于内置目录，两者都查，防止覆盖既有语义
	configured, err := config.ReadAgents(h.ConfigPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{"code": "read_config", "message": "failed to read config: " + err.Error()},
		})
		return
	}
	for _, a := range configured {
		if strings.EqualFold(a.ID, id) {
			c.JSON(http.StatusConflict, gin.H{
				"error": gin.H{"code": "agent_id_exists", "message": "agent id '" + id + "' already exists in config"},
			})
			return
		}
	}
	if _, builtin := providers.BuiltinTemplate(id); builtin {
		c.JSON(http.StatusConflict, gin.H{
			"error": gin.H{"code": "agent_id_exists", "message": "agent id '" + id + "' is a built-in agent"},
		})
		return
	}

	// 3. 二进制真实存在（绝对/相对路径查文件存在性，纯命令名查 PATH）
	if !providers.IsInstalled(command) {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{"code": "agent_command_not_found", "message": "command '" + command + "' does not exist on this machine"},
		})
		return
	}

	// 4/5. 组装配置（默认启用）→ 写盘 → 热更新注册
	cfg := config.AgentConfig{
		ID:        id,
		Name:      name,
		Enabled:   true,
		Transport: "stdio", // 当前唯一支持的传输方式，与 config.example.toml 对齐
		Command:   command,
		Args:      splitArgs(req.Args),
	}
	if err := config.SetAgentEnabled(h.ConfigPath, cfg, true); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{"code": "write_config", "message": "failed to update config: " + err.Error()},
		})
		return
	}
	if err := h.Mgr.SetAgentEnabled(cfg, true); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{"code": "hot_update_failed", "message": "config saved but runtime update failed: " + err.Error()},
		})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"ok": true,
		"agent": gin.H{
			"agentId": id,
			"name":    name,
			"command": command,
			"enabled": true,
			"source":  "config",
		},
	})
}

// splitArgs 把一行参数文本按 shell 词法切分为参数数组（不做变量展开/通配符/重定向）：
//   - 空白（空格/Tab/换行）分隔参数
//   - 单引号 '...' 内原样保留（无转义）
//   - 双引号 "..." 内空白保留，支持 \" 与 \\ 转义，其它反斜杠原样保留
//   - 引号外反斜杠转义下一字符（支持路径含空格等场景，如 "C:\Program Files\x" 里空格不拆开）
//   - 未闭合引号按词法继续并入剩余输入（尽力而为，不报错）
func splitArgs(line string) []string {
	var args []string
	var cur strings.Builder
	inSingle, inDouble := false, false
	flush := func() {
		if cur.Len() > 0 {
			args = append(args, cur.String())
			cur.Reset()
		}
	}
	runes := []rune(line)
	for i := 0; i < len(runes); i++ {
		ch := runes[i]
		switch {
		case inSingle:
			if ch == '\'' {
				inSingle = false
			} else {
				cur.WriteRune(ch)
			}
		case inDouble:
			switch ch {
			case '"':
				inDouble = false
			case '\\':
				if i+1 < len(runes) && (runes[i+1] == '"' || runes[i+1] == '\\') {
					i++
					cur.WriteRune(runes[i])
				} else {
					cur.WriteRune(ch)
				}
			default:
				cur.WriteRune(ch)
			}
		default:
			switch ch {
			case '\'':
				inSingle = true
			case '"':
				inDouble = true
			case ' ', '\t', '\n', '\r':
				flush()
			case '\\':
				if i+1 < len(runes) {
					i++
					cur.WriteRune(runes[i])
				} else {
					cur.WriteRune(ch)
				}
			default:
				cur.WriteRune(ch)
			}
		}
	}
	flush()
	return args
}
