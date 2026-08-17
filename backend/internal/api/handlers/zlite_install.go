package handlers

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/helloxz/zacp/internal/acp/providers"
)

// zliteInstallTimeout 设置页约定的安装超时：脚本需要下载二进制，网络慢时
// 可能需要较长时间，取 5 分钟（前端加载态与此对齐）。
const zliteInstallTimeout = 5 * time.Minute

// InstallZlite POST /api/v1/agents/zlite/install — 安装 zlite 官方智能体。
//
// 行为：按后端运行平台（GOOS）执行官方安装脚本（macOS/Linux 走 curl|bash，
// Windows 走 irm|iex，与主程序同机部署假设成立），安装到 ~/.zlite 目录。
// 这是远程代码执行类操作，前端必须经确认弹窗（提示 GitHub 可达性与安装路径）。
//
// 错误码：
//   - 400 agent_already_installed：本机已检测到 zlite（幂等拒绝）
//   - 400 unsupported_platform：当前平台无安装命令
//   - 409 installing_in_progress：已有安装任务进行中
//   - 504 zlite_install_timeout：超过 5 分钟未完成
//   - 500 zlite_install_failed：脚本执行失败（message 带输出尾部便于排查）
func (h *AgentManageHandler) InstallZlite(c *gin.Context) {
	if providers.IsInstalled("zlite") {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{"code": "agent_already_installed", "message": "zlite is already installed on this machine"},
		})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), zliteInstallTimeout)
	defer cancel()

	tail, err := providers.InstallZlite(ctx)
	if err != nil {
		switch {
		case errors.Is(err, providers.ErrZliteInstalling):
			c.JSON(http.StatusConflict, gin.H{
				"error": gin.H{"code": "installing_in_progress", "message": "another zlite install is already in progress"},
			})
		case errors.Is(err, context.DeadlineExceeded):
			c.JSON(http.StatusGatewayTimeout, gin.H{
				"error": gin.H{"code": "zlite_install_timeout", "message": "zlite install timed out after 5 minutes, please retry; make sure your network can reach GitHub"},
			})
		case errors.Is(err, context.Canceled):
			// 客户端断开/页面刷新：安装被取消，不再向客户端回写
			return
		default:
			message := "zlite install failed"
			if tail != "" {
				message += ": " + lastChars(tail, 500)
			}
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": gin.H{"code": "zlite_install_failed", "message": message},
			})
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// lastChars 截取字符串尾部最多 n 个 rune（展示安装脚本输出尾部，帮助排查失败原因）。
func lastChars(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[len(r)-n:])
}
