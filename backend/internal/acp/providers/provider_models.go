package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// UpstreamError 表示对上游 LLM Provider 的 HTTP 调用失败，携带状态码与响应片段。
// 上层 handler 据此映射为对前端友好的 HTTP 状态（401/404/504/502）。
type UpstreamError struct {
	Status int
	Body   string
}

func (e *UpstreamError) Error() string {
	if e.Body != "" {
		return fmt.Sprintf("upstream status %d: %s", e.Status, e.Body)
	}
	return fmt.Sprintf("upstream status %d", e.Status)
}

// buildUpstreamURL 按 baseURL 是否以 /v1 结尾智能拼接子路径，避免双重 /v1。
// subPath 必须以 /v1 开头（如 /v1/models、/v1/chat/completions）。
// - baseURL=https://api.example.com/v1 + subPath=/v1/models → https://api.example.com/v1/models
// - baseURL=https://api.example.com + subPath=/v1/models → https://api.example.com/v1/models
// - baseURL=https://api.example.com/openai/v1 + subPath=/v1/models → https://api.example.com/openai/v1/models
func buildUpstreamURL(baseURL, subPath string) string {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" || subPath == "" {
		return baseURL + subPath
	}
	if strings.HasSuffix(baseURL, "/v1") && strings.HasPrefix(subPath, "/v1") {
		return baseURL + subPath[3:] // 去掉 subPath 的 "/v1" 前缀
	}
	return baseURL + subPath
}

// validateProviderChannelCommon 校验 type/baseUrl/apiKey 的公共规则（供 models 与 test 复用）。
// 与 ValidateZliteChannel 保持一致的 baseUrl/apiKey 规则，但不要求 models。
func validateProviderChannelCommon(channelType, baseURL, apiKey string) (string, string, error) {
	if !ValidZliteChannelType(channelType) {
		return "", "", fmt.Errorf("type 必须是 openai.chat / openai.responses / anthropic 之一")
	}
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return "", "", fmt.Errorf("base_url 不能为空")
	}
	if !strings.HasPrefix(baseURL, "http://") && !strings.HasPrefix(baseURL, "https://") {
		return "", "", fmt.Errorf("base_url 必须以 http:// 或 https:// 开头")
	}
	if strings.ContainsAny(baseURL, "'\n") {
		return "", "", fmt.Errorf("base_url 不能包含单引号或换行")
	}
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return "", "", fmt.Errorf("api_key 不能为空")
	}
	if strings.Contains(apiKey, "\n") {
		return "", "", fmt.Errorf("api_key 不能包含换行")
	}
	return channelType, baseURL, nil
}

// FetchProviderModels 拉取上游可用模型列表，兼容 openai/anthropic。
// - openai.*：GET {baseURL}/v1/models + Authorization: Bearer
// - anthropic：GET {baseURL}/v1/models + x-api-key + anthropic-version
// 返回的 id 列表保持上游顺序，已去空、去重保序。
func FetchProviderModels(ctx context.Context, channelType, baseURL, apiKey string) ([]string, error) {
	if _, b, err := validateProviderChannelCommon(channelType, baseURL, apiKey); err != nil {
		return nil, err
	} else {
		baseURL = b
	}
	// 归一并构造上游 URL
	url := buildUpstreamURL(baseURL, "/v1/models")

	// 上下文超时 8s（调用方已带超时的 context 会叠加，以更早的为准）
	ctxTimeout, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctxTimeout, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if channelType == ZliteChannelTypeAnthropic {
		req.Header.Set("x-api-key", apiKey)
		req.Header.Set("anthropic-version", "2023-06-01")
	} else {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		// 超时/网络错误
		if ctxTimeout.Err() == context.DeadlineExceeded {
			return nil, &UpstreamError{Status: 504, Body: "upstream timeout"}
		}
		return nil, &UpstreamError{Status: 502, Body: err.Error()}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20)) // 2MB 上限
	if err != nil {
		return nil, &UpstreamError{Status: 502, Body: "读取上游响应失败"}
	}
	if resp.StatusCode != http.StatusOK {
		// 截断 body 避免过大
		msg := strings.TrimSpace(string(body))
		if len(msg) > 500 {
			msg = msg[:500]
		}
		return nil, &UpstreamError{Status: resp.StatusCode, Body: msg}
	}

	// 兼容 OpenAI 与 Anthropic 的 {data:[{id}]} 结构
	var generic struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &generic); err != nil {
		return nil, &UpstreamError{Status: 502, Body: "解析上游响应失败"}
	}
	if len(generic.Data) == 0 {
		return nil, &UpstreamError{Status: 502, Body: "上游返回模型列表为空"}
	}
	seen := make(map[string]bool, len(generic.Data))
	models := make([]string, 0, len(generic.Data))
	for _, d := range generic.Data {
		id := strings.TrimSpace(d.ID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		models = append(models, id)
	}
	if len(models) == 0 {
		return nil, &UpstreamError{Status: 502, Body: "上游返回模型列表为空"}
	}
	return models, nil
}

// TestProviderModel 试探指定模型是否可正常响应（最小 token 试探，hi / max_tokens 5）。
// 按 type 分发到不同端点：
// - openai.chat: POST /v1/chat/completions
// - openai.responses: POST /v1/responses
// - anthropic: POST /v1/messages
func TestProviderModel(ctx context.Context, channelType, baseURL, apiKey, model string) error {
	if _, b, err := validateProviderChannelCommon(channelType, baseURL, apiKey); err != nil {
		return err
	} else {
		baseURL = b
	}
	model = strings.TrimSpace(model)
	if model == "" {
		return fmt.Errorf("model 不能为空")
	}
	if strings.ContainsAny(model, "'\n") {
		return fmt.Errorf("model 不能包含单引号或换行")
	}

	var url string
	var bodyMap map[string]any

	switch channelType {
	case ZliteChannelTypeOpenAIChat:
		url = buildUpstreamURL(baseURL, "/v1/chat/completions")
		bodyMap = map[string]any{
			"model": model,
			"messages": []map[string]string{
				{"role": "user", "content": "hi"},
			},
			"max_tokens": 5,
		}
	case ZliteChannelTypeOpenAIResponse:
		url = buildUpstreamURL(baseURL, "/v1/responses")
		bodyMap = map[string]any{
			"model":             model,
			"input":             "hi",
			"max_output_tokens": 5,
		}
	case ZliteChannelTypeAnthropic:
		url = buildUpstreamURL(baseURL, "/v1/messages")
		bodyMap = map[string]any{
			"model":      model,
			"max_tokens": 5,
			"messages": []map[string]string{
				{"role": "user", "content": "hi"},
			},
		}
	default:
		return fmt.Errorf("不支持的 type: %s", channelType)
	}

	bodyBytes, err := json.Marshal(bodyMap)
	if err != nil {
		return fmt.Errorf("序列化请求失败: %w", err)
	}

	ctxTimeout, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctxTimeout, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("创建请求失败: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	if channelType == ZliteChannelTypeAnthropic {
		req.Header.Set("x-api-key", apiKey)
		req.Header.Set("anthropic-version", "2023-06-01")
	} else {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		if ctxTimeout.Err() == context.DeadlineExceeded {
			return &UpstreamError{Status: 504, Body: "upstream timeout"}
		}
		return &UpstreamError{Status: 502, Body: err.Error()}
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return &UpstreamError{Status: 502, Body: "读取上游响应失败"}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(respBody))
		if len(msg) > 800 {
			msg = msg[:800]
		}
		return &UpstreamError{Status: resp.StatusCode, Body: msg}
	}
	// 2xx 视为成功，不再强校验响应体结构（不同 provider 结构差异大）
	return nil
}
