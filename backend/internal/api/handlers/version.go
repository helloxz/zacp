package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/zacp/zacp/internal/version"
)

// GetVersion 返回服务端构建版本信息（GET /api/v1/version）。
// 版本号由 scripts/build.sh 构建时通过 -ldflags 注入（来源：frontend/package.json 的 version 字段）；
// 前端设置页展示的版本号即来自此接口，避免硬编码漂移。
func GetVersion(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"version":   version.Version,
		"commit":    version.Commit,
		"buildTime": version.BuildTime,
	})
}
