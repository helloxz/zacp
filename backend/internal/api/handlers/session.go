package handlers

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/helloxz/zacp/internal/service"
	"github.com/helloxz/zacp/internal/ws"
)

// SessionHandler 会话 HTTP 处理器
type SessionHandler struct {
	svc    *service.SessionService
	bridge *ws.EventBridge
}

// NewSessionHandler 创建会话处理器
func NewSessionHandler(svc *service.SessionService, bridge *ws.EventBridge) *SessionHandler {
	return &SessionHandler{svc: svc, bridge: bridge}
}

// CreateSession 创建新会话
// POST /api/v1/sessions
// workspaceId 可选：为 0 / 缺省时回退默认工作区（见 service.resolveWorkspace）。
// isDraft 可选：true 表示隐式草稿会话（预览配置项，不进侧栏），默认 false。
// 响应携带 session 与 agent 下发的 configOptions，供前端空态直接展示。
func (h *SessionHandler) CreateSession(c *gin.Context) {
	var req struct {
		WorkspaceID uint   `json:"workspaceId"`
		AgentID     string `json:"agentId" binding:"required"`
		IsDraft     bool   `json:"isDraft"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{"code": "invalid_request", "message": err.Error()},
		})
		return
	}

	// 提前注册「按通知 sessionId 分发」的 session/update 处理器（configOptions /
	// availableCommands）：reasonix 等 agent 在 session/new 响应同一时刻同步推送
	// available_commands_update，若等创建完成再注册会因 SDK read loop 竞态丢通知
	// （omp 有 ~50ms 延迟所以此前未暴露）。失败不阻断：下方成功后仍会完整注册兜底。
	if h.bridge != nil {
		if err := h.bridge.EnsureSessionUpdateHandlers(req.AgentID); err != nil {
			h.bridge.Log().Warn("pre-register session update handlers failed",
				"agentID", req.AgentID, "err", err)
		}
	}

	result, err := h.svc.CreateSession(c.Request.Context(), req.WorkspaceID, req.AgentID, req.IsDraft)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{"code": "create_session_failed", "message": err.Error()},
		})
		return
	}

	// 完整注册事件回调（含 available_commands_update 处理器），并把 onEvent / 权限等
	// 依赖会话的闭包绑定到真实 ACP session id（幂等，覆盖上一步的提前注册）。
	// 部分 agent（如 omp）在 session/new 响应返回后很快（几十 ms）就推送可用命令通告，
	// 若等用户首条 prompt 才注册（见 EventBridge.HandlePrompt）就会错过，
	// 导致 /slash-commands 一直是空。注册动作幂等，后续 prompt 前仍会覆盖注册。
	if h.bridge != nil {
		if err := h.bridge.SetupEventCallback(req.AgentID, result.Session.ACPSessionID); err != nil {
			// 注册失败不阻断创建：回调会在首个 prompt 前重试注册
			h.bridge.Log().Warn("setup event callback after session create failed",
				"agentID", req.AgentID, "acpSessionID", result.Session.ACPSessionID, "err", err)
		}
	}

	c.JSON(http.StatusCreated, gin.H{
		"session":       result.Session,
		"configOptions": result.ConfigOptions,
	})
}

// GetSession 获取会话详情
// GET /api/v1/sessions/:id
func (h *SessionHandler) GetSession(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{"code": "invalid_id", "message": "invalid session id"},
		})
		return
	}

	session, err := h.svc.GetSession(uint(id))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"error": gin.H{"code": "session_not_found", "message": err.Error()},
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"session": session})
}

// ListSessions 列出工作目录下的所有会话
// GET /api/v1/workspaces/:id/sessions
func (h *SessionHandler) ListSessions(c *gin.Context) {
	workspaceID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{"code": "invalid_id", "message": "invalid workspace id"},
		})
		return
	}

	sessions, err := h.svc.ListSessions(uint(workspaceID))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{"code": "list_sessions_failed", "message": err.Error()},
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"sessions": sessions})
}

// ListRecentSessions 列出最近活跃的会话（全局，侧栏数据源）
// GET /api/v1/sessions?limit=50
func (h *SessionHandler) ListRecentSessions(c *gin.Context) {
	limit := 50
	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	sessions, err := h.svc.ListRecentSessions(limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{"code": "list_sessions_failed", "message": err.Error()},
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"sessions": sessions})
}

// GetConfigOptions 获取会话配置项（模型/思考强度/mode 等）
// GET /api/v1/sessions/:id/config-options
func (h *SessionHandler) GetConfigOptions(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{"code": "invalid_id", "message": "invalid session id"},
		})
		return
	}

	opts, err := h.svc.GetConfigOptions(uint(id))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"error": gin.H{"code": "session_not_found", "message": err.Error()},
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"configOptions": opts})
}

// GetSlashCommands 获取会话可用 / 命令（agent 经 available_commands_update 通告）
// GET /api/v1/sessions/:id/slash-commands
func (h *SessionHandler) GetSlashCommands(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{"code": "invalid_id", "message": "invalid session id"},
		})
		return
	}

	cmds, err := h.svc.GetSlashCommands(uint(id))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"error": gin.H{"code": "session_not_found", "message": err.Error()},
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"slashCommands": cmds})
}

// SetConfigOption 设置会话配置项（如切换模型/思考强度/mode）
// POST /api/v1/sessions/:id/config-options
func (h *SessionHandler) SetConfigOption(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{"code": "invalid_id", "message": "invalid session id"},
		})
		return
	}

	var req struct {
		OptionID string `json:"optionId" binding:"required"`
		ValueID  string `json:"valueId" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{"code": "invalid_request", "message": err.Error()},
		})
		return
	}

	if err := h.svc.SetConfigOption(c.Request.Context(), uint(id), req.OptionID, req.ValueID); err != nil {
		// 区分错误语义：会话不存在 404、未建立 ACP 连接 409、
		// agent 拒绝（值无效/选项未知，属客户端参数问题）400，其余为服务器错误 500
		switch {
		case errors.Is(err, service.ErrSessionNotFound):
			c.JSON(http.StatusNotFound, gin.H{
				"error": gin.H{"code": "session_not_found", "message": err.Error()},
			})
		case errors.Is(err, service.ErrNoACPSession):
			c.JSON(http.StatusConflict, gin.H{
				"error": gin.H{"code": "no_acp_session", "message": err.Error()},
			})
		default:
			c.JSON(http.StatusBadRequest, gin.H{
				"error": gin.H{"code": "set_config_option_failed", "message": err.Error()},
			})
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// RenameSession 重命名会话标题（用户手动重命名，仅更新本地 DB）
// PATCH /api/v1/sessions/:id   body: { "title": "..." }
func (h *SessionHandler) RenameSession(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{"code": "invalid_id", "message": "invalid session id"},
		})
		return
	}

	var req struct {
		Title string `json:"title" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{"code": "invalid_request", "message": err.Error()},
		})
		return
	}

	if err := h.svc.RenameSession(uint(id), req.Title); err != nil {
		switch {
		case errors.Is(err, service.ErrSessionNotFound):
			c.JSON(http.StatusNotFound, gin.H{
				"error": gin.H{"code": "session_not_found", "message": err.Error()},
			})
		case errors.Is(err, service.ErrInvalidArgument):
			// 标题非法（空/超长等）属客户端参数问题，400
			c.JSON(http.StatusBadRequest, gin.H{
				"error": gin.H{"code": "invalid_title", "message": err.Error()},
			})
		default:
			// DB 故障等服务器错误，500（避免前端误判为参数问题）
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": gin.H{"code": "rename_session_failed", "message": err.Error()},
			})
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// DeleteSession 删除会话
// DELETE /api/v1/sessions/:id
func (h *SessionHandler) DeleteSession(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{"code": "invalid_id", "message": "invalid session id"},
		})
		return
	}

	if err := h.svc.DeleteSession(uint(id)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{"code": "delete_session_failed", "message": err.Error()},
		})
		return
	}

	c.JSON(http.StatusNoContent, nil)
}

// DeleteDraftSession 删除草稿会话（切 tab / 离开空态时释放旧隐式草稿）
// DELETE /api/v1/sessions/:id/draft
func (h *SessionHandler) DeleteDraftSession(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{"code": "invalid_id", "message": "invalid session id"},
		})
		return
	}

	if err := h.svc.DeleteDraftSession(uint(id)); err != nil {
		switch {
		case errors.Is(err, service.ErrSessionNotFound):
			c.JSON(http.StatusNotFound, gin.H{
				"error": gin.H{"code": "session_not_found", "message": err.Error()},
			})
		case errors.Is(err, service.ErrInvalidArgument):
			// 目标不是草稿：必须走正常删除路径，400
			c.JSON(http.StatusBadRequest, gin.H{
				"error": gin.H{"code": "not_a_draft", "message": err.Error()},
			})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": gin.H{"code": "delete_draft_failed", "message": err.Error()},
			})
		}
		return
	}

	c.JSON(http.StatusNoContent, nil)
}

// SendMessage 发送消息到会话
// POST /api/v1/sessions/:id/messages
func (h *SessionHandler) SendMessage(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{"code": "invalid_id", "message": "invalid session id"},
		})
		return
	}

	var req struct {
		Content string `json:"content" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{"code": "invalid_request", "message": err.Error()},
		})
		return
	}

	message, err := h.svc.SendMessage(c.Request.Context(), uint(id), req.Content)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{"code": "send_message_failed", "message": err.Error()},
		})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"message": message})
}

// GetMessages 获取会话最新消息窗口；分页从最新消息端计算，响应内仍按时间正序排列。
// GET /api/v1/sessions/:id/messages
func (h *SessionHandler) GetMessages(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{"code": "invalid_id", "message": "invalid session id"},
		})
		return
	}
	if rawAfterID := c.Query("afterId"); rawAfterID != "" {
		afterID, parseErr := strconv.ParseUint(rawAfterID, 10, 32)
		if parseErr != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": gin.H{"code": "invalid_after_id", "message": "invalid afterId"},
			})
			return
		}

		messages, queryErr := h.svc.GetMessagesAfter(uint(id), uint(afterID))
		if queryErr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": gin.H{"code": "get_message_updates_failed", "message": queryErr.Error()},
			})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"messages": messages,
			"afterId":  afterID,
		})
		return
	}
	// 分页参数
	limit := 50
	offset := 0

	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	if o := c.Query("offset"); o != "" {
		if parsed, err := strconv.Atoi(o); err == nil && parsed >= 0 {
			offset = parsed
		}
	}

	messages, err := h.svc.GetMessages(uint(id), limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{"code": "get_messages_failed", "message": err.Error()},
		})
		return
	}

	// 获取总数
	total, err := h.svc.CountMessages(uint(id))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{"code": "count_messages_failed", "message": err.Error()},
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"messages": messages,
		"total":    total,
		"limit":    limit,
		"offset":   offset,
	})
}

// GetMessageThoughts 获取单条消息的思考过程文本（按需加载）。
// GET /api/v1/sessions/:id/messages/:messageId/thoughts
// 消息列表接口已把 agent_thought 的 text 置空瘦身（保留 type 供前端判断存在性），
// 前端展开思考过程折叠面板时调用本接口恢复完整内容；消息必须属于该会话。
func (h *SessionHandler) GetMessageThoughts(c *gin.Context) {
	sessionID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{"code": "invalid_id", "message": "invalid session id"},
		})
		return
	}
	messageID, err := strconv.ParseUint(c.Param("messageId"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{"code": "invalid_message_id", "message": "invalid message id"},
		})
		return
	}

	reasoning, err := h.svc.GetMessageThoughts(uint(sessionID), uint(messageID))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{
				"error": gin.H{"code": "message_not_found", "message": "message not found"},
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{"code": "get_message_thoughts_failed", "message": err.Error()},
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"reasoning": reasoning})
}
