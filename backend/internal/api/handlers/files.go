package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/helloxz/zacp/internal/auth"
	"github.com/helloxz/zacp/internal/service"
)

// FileHandler 工作区文件浏览 / 上传 / 重命名 / 原始内容读取。
//
// 所有接口都以 workspace 为安全边界：相对路径由 FileService 统一校验，
// 保证任何读写都落在 workspace.Path 之内（含 symlink 逃逸防护）。
type FileHandler struct {
	svc *service.FileService
	// authSvc 用于签发/校验文件直链资源 token（预览换 12 小时短 token）。
	authSvc *auth.Service
}

type renameFileRequest struct {
	Path string `json:"path"`
	Name string `json:"name"`
}

// NewFileHandler 创建文件处理器。
// authSvc 可为 nil（认证未启用时 preview-token 端点也应可用，但默认不开放直链——
// 见 PreviewToken 中的说明）。
func NewFileHandler(svc *service.FileService, authSvc *auth.Service) *FileHandler {
	return &FileHandler{svc: svc, authSvc: authSvc}
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

// RenameFile PATCH /api/v1/workspaces/:id/files/rename
//
// 请求体为 {"path":"src/old.txt","name":"new.txt"}；只允许修改 basename，
// 不允许借此接口把条目移动到其他目录。
func (h *FileHandler) RenameFile(c *gin.Context) {
	id, err := parseWorkspaceID(c)
	if err != nil {
		writeError(c, http.StatusBadRequest, "invalid_workspace_id", err.Error())
		return
	}
	var req renameFileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	file, err := h.svc.RenameFile(id, req.Path, req.Name)
	if err != nil {
		writeFileError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"file": file})
}

type deleteFileRequest struct {
	Path string `json:"path"`
}

// Delete DELETE /api/v1/workspaces/:id/files
//
// 删除工作区内的文件或目录（目录递归删除，不可恢复）。
// 请求体为 {"path":"src/old.txt"}。
//
// 安全前置：删除是破坏性操作，未启用登录认证（Auth.PasswordHash 未配置）时
// 一律 403 拒绝，防止无认证部署下被任意调用方直接删文件。
// 路径防穿透（`.` / `..` 段、根路径、绝对路径、symlink 逃逸、.git 等
// 受保护目录）由 FileService.DeleteFile 严格校验。
func (h *FileHandler) Delete(c *gin.Context) {
	id, err := parseWorkspaceID(c)
	if err != nil {
		writeError(c, http.StatusBadRequest, "invalid_workspace_id", err.Error())
		return
	}
	if h.authSvc == nil || !h.authSvc.Enabled() {
		writeError(c, http.StatusForbidden, "auth_required", "未启用登录认证，禁止删除文件")
		return
	}
	var req deleteFileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if err := h.svc.DeleteFile(id, req.Path); err != nil {
		writeFileError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": true})
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
			writeError(c, http.StatusRequestEntityTooLarge, "upload_body_too_large", "上传内容超过 11MB 上限")
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

// UploadTemp POST /api/v1/files/upload-temp
//
// 聊天输入框快捷键（Ctrl/Cmd+V）粘贴上传专用：文件写入系统临时目录
// /tmp/{yyyyMMddHH}/（目录由后端生成，不接受客户端路径），返回绝对路径列表，
// 前端据此填充 @/tmp/... 引用。与工作区上传（/workspaces/:id/files/upload）不同：
// 允许同名覆盖、不依赖 workspace、返回绝对路径；临时目录不做清理，交由系统回收。
//
// multipart 表单：files（一个或多个文件字段）。无 dir 参数。
func (h *FileHandler) UploadTemp(c *gin.Context) {
	// 请求体整体限流，防止超大 multipart 打爆内存/磁盘（与工作区上传同一上限）
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, service.MaxUploadBodyBytes)
	if err := c.Request.ParseMultipartForm(4 << 20); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(c, http.StatusRequestEntityTooLarge, "upload_body_too_large", "上传内容超过 11MB 上限")
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

	results, err := h.svc.UploadTempFiles(files)
	if err != nil {
		writeFileError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"files": results})
}

// PreviewToken POST /api/v1/workspaces/:id/files/preview-token
//
// 换取文件直链（raw URL + 12 小时资源 token，绑定 workspace+path）。
// 直链供 <img src> 等无法携带自定义 header 的场景使用；资源 token 与主登录 token
// 分离，即使出现在访问日志中也不会泄露登录态。
// 请求体 {"path":"<相对路径>"}，返回 {"url":"<相对路径，含 ?token=>"}。
type previewTokenRequest struct {
	Path string `json:"path"`
}

func (h *FileHandler) PreviewToken(c *gin.Context) {
	if h.authSvc == nil {
		writeError(c, http.StatusBadRequest, "preview_token_unavailable", "直链服务不可用")
		return
	}
	id, err := parseWorkspaceID(c)
	if err != nil {
		writeError(c, http.StatusBadRequest, "invalid_workspace_id", err.Error())
		return
	}
	var req previewTokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	// 先做与 RawFile 一致的路径合法性/存在性校验（防穿越），
	// 并复用下载上限校验（≤100MB）：超限文件提前 413，避免签发 token 后
	// 在浏览器原生下载/预览阶段才静默失败（图片预览破图、下载无声无息）。
	if _, err := h.svc.ResolveFileForDownload(id, req.Path); err != nil {
		writeFileError(c, err)
		return
	}
	token := h.authSvc.IssueResourceToken(strconv.FormatUint(uint64(id), 10), req.Path)
	url := fmt.Sprintf("/api/v1/workspaces/%d/files/raw?path=%s&token=%s",
		id, url.QueryEscape(req.Path), token)
	c.JSON(http.StatusOK, gin.H{"url": url})
}

// RawFile GET /api/v1/workspaces/:id/files/raw?path=<相对路径>[&download=1]
//
// 返回文件原始字节（Content-Type 按扩展名推断），供前端图片预览 / 文件下载。
// 与预览共用同一端点：携带 ≤100MB 下载上限（见 ResolveFileForDownload），
// 超限返回 413，防止工作区大文件被整包拉走或经预览绕过下载限制。
// `download=1` 时返回 Content-Disposition: attachment，强制浏览器落盘——
// 跨源部署下前端 `<a download>` 属性会被浏览器忽略（仅同源/blob URL 生效），
// 文本类直链会退化为当前窗口内联渲染，必须靠附件响应头触发下载。
func (h *FileHandler) RawFile(c *gin.Context) {
	id, err := parseWorkspaceID(c)
	if err != nil {
		writeError(c, http.StatusBadRequest, "invalid_workspace_id", err.Error())
		return
	}
	path, err := h.svc.ResolveFileForDownload(id, c.Query("path"))
	if err != nil {
		writeFileError(c, err)
		return
	}
	// 已通过边界 + ≤100MB 校验，可安全交给静态文件服务。
	// 预览（<img src> 子资源加载）不传 download=1，维持 inline；
	// 下载场景传 download=1，文件名取真实 basename（与工作区条目名一致）。
	if c.Query("download") == "1" {
		c.FileAttachment(path, filepath.Base(path))
		return
	}
	c.File(path)
}

// writeFileContentRequest 文本文件保存请求体。
type writeFileContentRequest struct {
	Path string `json:"path"`
	// Content 编辑后的完整文本内容（后端校验 ≤2MB 且为合法 UTF-8）。
	Content string `json:"content"`
	// ExpectedMtime 打开时记录的 mtime（毫秒）；可选，携带时后端做乐观锁比对。
	ExpectedMtime *int64 `json:"expectedMtime,omitempty"`
}

// ReadFileContent GET /api/v1/workspaces/:id/files/content?path=<相对路径>
//
// 返回文本文件内容（Content + MtimeUnixMs），供前端编辑器打开。
// 目录 / 超过 2MB / 二进制 / 非 UTF-8 的文件分别返回对应错误，由前端提示。
func (h *FileHandler) ReadFileContent(c *gin.Context) {
	id, err := parseWorkspaceID(c)
	if err != nil {
		writeError(c, http.StatusBadRequest, "invalid_workspace_id", err.Error())
		return
	}
	content, err := h.svc.ReadTextFile(id, c.Query("path"))
	if err != nil {
		writeFileError(c, err)
		return
	}
	c.JSON(http.StatusOK, content)
}

// WriteFileContent PUT /api/v1/workspaces/:id/files/content
//
// 保存文本文件。携带 expectedMtime 时校验乐观锁：文件已被他处修改则 409，
// 前端据此提示「文件已变更」并让用户选择重新加载。
func (h *FileHandler) WriteFileContent(c *gin.Context) {
	id, err := parseWorkspaceID(c)
	if err != nil {
		writeError(c, http.StatusBadRequest, "invalid_workspace_id", err.Error())
		return
	}
	var req writeFileContentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	result, err := h.svc.WriteTextFile(id, req.Path, req.Content, req.ExpectedMtime)
	if err != nil {
		writeFileError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
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

func writeFileError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrPathOutsideWorkspace):
		writeError(c, http.StatusBadRequest, "path_outside_workspace", "路径超出工作区范围")
	case errors.Is(err, service.ErrCannotRenameRoot):
		writeError(c, http.StatusBadRequest, "cannot_rename_root", "不能重命名工作区根目录")
	case errors.Is(err, service.ErrCannotDeleteRoot):
		writeError(c, http.StatusBadRequest, "cannot_delete_root", "不能删除工作区根目录")
	case errors.Is(err, service.ErrInvalidPathSegments):
		writeError(c, http.StatusBadRequest, "invalid_path_segments", "路径包含 . 或 .. 段，禁止删除")
	case errors.Is(err, service.ErrCannotDeleteIgnoredDir):
		writeError(c, http.StatusBadRequest, "cannot_delete_ignored_dir", "该目录受保护（.git、node_modules 等），禁止删除")
	case errors.Is(err, service.ErrNotDirectory):
		writeError(c, http.StatusBadRequest, "not_directory", "路径类型不匹配（目录 / 文件不符）")
	case errors.Is(err, service.ErrFileExists):
		writeError(c, http.StatusConflict, "file_exists", "同名文件已存在，拒绝覆盖")
	case errors.Is(err, service.ErrInvalidFileName):
		writeError(c, http.StatusBadRequest, "invalid_file_name", "文件名不合法")
	case errors.Is(err, service.ErrFileTooLarge):
		writeError(c, http.StatusRequestEntityTooLarge, "file_too_large", "文件超过大小上限（图片 5MB / 其他 10MB）")
	case errors.Is(err, service.ErrFileTooLargeForEdit):
		writeError(c, http.StatusRequestEntityTooLarge, "file_too_large", "文件超过 2MB，不支持文本编辑")
	case errors.Is(err, service.ErrDownloadTooLarge):
		writeError(c, http.StatusRequestEntityTooLarge, "download_too_large", "文件超过 100M，不支持下载")
	case errors.Is(err, service.ErrBinaryFile):
		writeError(c, http.StatusUnsupportedMediaType, "binary_file", "二进制文件不支持文本编辑")
	case errors.Is(err, service.ErrNotEditableFile):
		writeError(c, http.StatusUnsupportedMediaType, "not_editable_file", "非常见文本文件，不支持文本编辑")
	case errors.Is(err, service.ErrInvalidEncoding):
		writeError(c, http.StatusUnsupportedMediaType, "invalid_encoding", "文件不是合法 UTF-8 编码，不支持编辑")
	case errors.Is(err, service.ErrFileModified):
		writeError(c, http.StatusConflict, "file_modified", "文件已被其他端修改，请重新加载后再保存")
	case errors.Is(err, service.ErrPathNotFound):
		writeError(c, http.StatusNotFound, "path_not_found", "路径不存在")
	case errors.Is(err, os.ErrPermission):
		writeError(c, http.StatusForbidden, "permission_denied", "没有权限访问该目录")
	default:
		writeError(c, http.StatusInternalServerError, "file_error", "文件操作失败: "+err.Error())
	}
}
