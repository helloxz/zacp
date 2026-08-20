package handlers

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/helloxz/zacp/internal/acp/providers"
)

// isHTMLResponse 判断上游 body 是否为 HTML（openresty 404 等），用于替换为友好英文。
func isHTMLResponse(s string) bool {
	ls := strings.ToLower(s)
	return strings.Contains(ls, "<html") || strings.Contains(ls, "<!doctype")
}

// providerModelsRequest 通用上游模型列表请求体（POST /api/v1/providers/models）。
// 与 zlite 的 type 枚举复用（openai.chat / openai.responses / anthropic）。
type providerModelsRequest struct {
	Type    string `json:"type" binding:"required"`
	BaseURL string `json:"baseUrl" binding:"required"`
	APIKey  string `json:"apiKey" binding:"required"`
}

// providerModelTestRequest 单模型可用性试探请求体（POST /api/v1/providers/models/test）。
type providerModelTestRequest struct {
	Type    string `json:"type" binding:"required"`
	BaseURL string `json:"baseUrl" binding:"required"`
	APIKey  string `json:"apiKey" binding:"required"`
	Model   string `json:"model" binding:"required"`
}

// ListProviderModels 通用获取上游可用模型列表（POST /api/v1/providers/models）。
// 兼容 openai.chat / openai.responses / anthropic，内部自动拼接 /v1/models 并携带对应鉴权头。
// 成功返回 {models: string[]}，失败按上游状态映射为对前端友好的 code。
func (h *AgentManageHandler) ListProviderModels(c *gin.Context) {
	var req providerModelsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{"code": "bad_request", "message": "body requires {\"type\", \"baseUrl\", \"apiKey\"}"},
		})
		return
	}
	models, err := providers.FetchProviderModels(c.Request.Context(), req.Type, req.BaseURL, req.APIKey)
	if err != nil {
		var ue *providers.UpstreamError
		if errors.As(err, &ue) {
			switch ue.Status {
			case 401, 403:
				c.JSON(http.StatusUnauthorized, gin.H{"error": gin.H{"code": "upstream_auth_failed", "message": ue.Body}})
				return
			case 404:
				// 上游常返回 openresty HTML（404 Not Found），对用户不友好，固定为英文文案
				c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"code": "models_not_supported", "message": "This provider does not support listing models. Please enter the model name manually."}})
				return
			case 504:
				c.JSON(http.StatusGatewayTimeout, gin.H{"error": gin.H{"code": "upstream_timeout", "message": "Upstream timeout"}})
				return
			default:
				// 若上游返回 HTML（如 openresty），同样替换为通用英文，避免前端展示 HTML
				msg := ue.Body
				if isHTMLResponse(msg) {
					msg = "Upstream error. Please check the provider configuration."
				}
				c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"code": "upstream_error", "message": msg}})
				return
			}
		}
		// 校验类错误（type/baseUrl/apiKey 非法）
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"code": "invalid_provider_channel", "message": err.Error()}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"models": models})
}

// TestProviderModel 试探指定模型是否可正常响应（POST /api/v1/providers/models/test）。
// 按 type 分发到 chat/completions、responses、messages 的最小 token 试探（hi / max_tokens 5）。
func (h *AgentManageHandler) TestProviderModel(c *gin.Context) {
	var req providerModelTestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{"code": "bad_request", "message": "body requires {\"type\", \"baseUrl\", \"apiKey\", \"model\"}"},
		})
		return
	}
	if err := providers.TestProviderModel(c.Request.Context(), req.Type, req.BaseURL, req.APIKey, req.Model); err != nil {
		var ue *providers.UpstreamError
		if errors.As(err, &ue) {
			switch ue.Status {
			case 401, 403:
				msg := ue.Body
				if isHTMLResponse(msg) {
					msg = "Authentication failed. Please check the API key."
				}
				c.JSON(http.StatusUnauthorized, gin.H{"error": gin.H{"code": "upstream_auth_failed", "message": msg}})
				return
			case 404:
				// 404 可能是模型不存在或端点不存在，若为 HTML 则固定为英文
				msg := ue.Body
				if isHTMLResponse(msg) || strings.TrimSpace(msg) == "" {
					msg = "Model not found or provider does not support this operation. Please check the model name."
				}
				c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"code": "model_not_found", "message": msg}})
				return
			case 429:
				c.JSON(http.StatusTooManyRequests, gin.H{"error": gin.H{"code": "upstream_rate_limited", "message": ue.Body}})
				return
			case 504:
				c.JSON(http.StatusGatewayTimeout, gin.H{"error": gin.H{"code": "upstream_timeout", "message": "Upstream timeout"}})
				return
			default:
				msg := ue.Body
				if isHTMLResponse(msg) {
					msg = "Upstream error. Please check the provider configuration."
				}
				c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"code": "upstream_error", "message": msg}})
				return
			}
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"code": "invalid_provider_channel", "message": err.Error()}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
