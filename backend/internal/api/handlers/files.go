package handlers

import (
	"errors"
	"net/http"
	"os"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/helloxz/zacp/internal/service"
)

// FileHandler 工作区文件浏览 / 上传 / 原始内容读取。
//
// 所有接口都以 workspace 为安全边界：相对路径由 FileService 统一校验，
// 保证任何读写都落在 workspace.Path 之内（含 symlink 逃逸防护）。
type FileHandler struct {
	svc *service.FileService
}

// NewFileHandler 创建文件处理器。
func NewFileHandler(svc *service.FileService) *FileHandler {
	return &FileHandler{svc: svc}
}

// ListFiles GET /api/v1/workspaces/:id/files?path=<相对目录>
//
// 列出工作区某目录的内容；path 省略表示工作区根。
// 隐藏文件由后端强制过滤（无开关），见 FileService.ListDir。
func (h *FileHandler) ListFiles(c *gin.Context) {
	id, err := parseWorkspaceID(c)
	if err != nil {
		writeError(c, http.StatusBadRequest, "invalid_workspace_id", err.Error())
		return
	}

	result, err := h.svc.ListDir(id, c.Query("path"))
	if err != nil {
		writeFileError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// ListDirectories GET /api/v1/fs/directories?path=<绝对路径>
//
// 新建项目弹窗的目录浏览：列出 path 下的子文件夹（仅文件夹，隐藏目录/大目录
// 由后端过滤）。path 省略时返回 session.default_cwd 解析后的绝对路径作为初始目录。
// 返回 { path, parent, entries }，前端据此展示面包屑与返回上级。
func (h *FileHandler) ListDirectories(c *gin.Context) {
	result, err := h.svc.ListDirectories(c.Query("path"))
	if err != nil {
		writeFileError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// Upload POST /api/v1/workspaces/:id/files/upload
//
// multipart 表单：
//   - dir：目标相对目录（省略 = 工作区根）
//   - files：一个或多个文件字段
//
// 返回落盘后的条目列表。同名文件已存在时整体拒绝（409），防误覆盖源码。
func (h *FileHandler) Upload(c *gin.Context) {
	id, err := parseWorkspaceID(c)
	if err != nil {
		writeError(c, http.StatusBadRequest, "invalid_workspace_id", err.Error())
		return
	}

	// 请求体整体限流，防止超大 multipart 打爆内存/磁盘
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, service.MaxUploadBodyBytes)
	if err := c.Request.ParseMultipartForm(4 << 20); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(c, http.StatusRequestEntityTooLarge, "upload_body_too_large", "上传内容超过 25MB 上限")
			return
		}
		writeError(c, http.StatusBadRequest, "invalid_multipart", "multipart 表单解析失败")
		return
	}

	headers := c.Request.MultipartForm.File["files"]
	if len(headers) == 0 {
		writeError(c, http.StatusBadRequest, "missing_files", "缺少 files 字段")
		return
	}

	// 提取上传文件：立即打开 reader，后续由 service 逐个写入
	files := make([]service.UploadFile, 0, len(headers))
	for _, fh := range headers {
		src, err := fh.Open()
		if err != nil {
			writeError(c, http.StatusBadRequest, "invalid_file", "文件读取失败: "+err.Error())
			return
		}
		defer src.Close()
		files = append(files, service.UploadFile{
			Name:     fh.Filename,
			MimeType: fh.Header.Get("Content-Type"),
			Size:     fh.Size,
			Reader:   src,
		})
	}

	results, err := h.svc.UploadFiles(id, c.PostForm("dir"), files)
	if err != nil {
		writeFileError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"files": results})
}

// RawFile GET /api/v1/workspaces/:id/files/raw?path=<相对路径>
//
// 返回文件原始字节（Content-Type 按扩展名推断），供前端图片预览 / 文件下载。
func (h *FileHandler) RawFile(c *gin.Context) {
	id, err := parseWorkspaceID(c)
	if err != nil {
		writeError(c, http.StatusBadRequest, "invalid_workspace_id", err.Error())
		return
	}
	path, err := h.svc.ResolveFile(id, c.Query("path"))
	if err != nil {
		writeFileError(c, err)
		return
	}
	// 已通过 symlink 逃逸校验，可安全交给静态文件服务
	c.File(path)
}

// parseWorkspaceID 解析路径参数 :id 为 uint。
func parseWorkspaceID(c *gin.Context) (uint, error) {
	raw := c.Param("id")
	id64, err := strconv.ParseUint(raw, 10, 32)
	if err != nil {
		return 0, errors.New("workspace id 必须是数字")
	}
	return uint(id64), nil
}

// writeError 输出统一错误结构 {error:{code,message}}（与现有 handler 惯例一致）。
func writeError(c *gin.Context, status int, code, message string) {
	c.JSON(status, gin.H{
		"error": gin.H{"code": code, "message": message},
	})
}

// writeFileError 把文件服务错误映射为统一错误结构。
func writeFileError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrPathOutsideWorkspace):
		writeError(c, http.StatusBadRequest, "path_outside_workspace", "路径超出工作区范围")
	case errors.Is(err, service.ErrNotDirectory):
		writeError(c, http.StatusBadRequest, "not_directory", "目标不是目录")
	case errors.Is(err, service.ErrFileExists):
		writeError(c, http.StatusConflict, "file_exists", "同名文件已存在，拒绝覆盖")
	case errors.Is(err, service.ErrInvalidFileName):
		writeError(c, http.StatusBadRequest, "invalid_file_name", "文件名不合法")
	case errors.Is(err, service.ErrFileTooLarge):
		writeError(c, http.StatusRequestEntityTooLarge, "file_too_large", "文件超过大小上限（图片 5MB / 其他 20MB）")
	case errors.Is(err, service.ErrPathNotFound):
		writeError(c, http.StatusNotFound, "path_not_found", "路径不存在")
	case errors.Is(err, os.ErrPermission):
		writeError(c, http.StatusForbidden, "permission_denied", "没有权限访问该目录")
	default:
		writeError(c, http.StatusInternalServerError, "file_error", "文件操作失败: "+err.Error())
	}
}
