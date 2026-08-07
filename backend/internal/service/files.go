package service

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/helloxz/zacp/internal/model"
	"github.com/helloxz/zacp/internal/store"
)

// ---------------------------------------------------------------------------
// FileService：工作区文件浏览与上传
//
// 安全模型（重要不变量）：
// - 所有相对路径先 Clean，拒绝绝对路径与 `..` 逃逸；
// - 拼接后 EvalSymlinks 解析真实路径，最终必须仍落在工作区根之内，
//   防止通过符号链接把读写引到工作区外（路径穿越 / symlink 逃逸）。
// ---------------------------------------------------------------------------

// 文件操作相关错误（handler 层映射为统一错误结构）。
var (
	// ErrPathOutsideWorkspace 相对路径越界（`..` / 绝对路径 / symlink 逃逸）。
	ErrPathOutsideWorkspace = errors.New("path escapes workspace")
	// ErrNotDirectory 目标不是目录。
	ErrNotDirectory = errors.New("target is not a directory")
	// ErrFileExists 目标文件已存在（拒绝覆盖，防误覆盖源码）。
	ErrFileExists = errors.New("file already exists")
	// ErrInvalidFileName 文件名非法（空、`.`、`..`、含路径分隔符）。
	ErrInvalidFileName = errors.New("invalid file name")
	// ErrFileTooLarge 单文件超过大小上限（图片 5MB / 其他 20MB）。
	ErrFileTooLarge = errors.New("file too large")
	// ErrPathNotFound 目标路径不存在。
	ErrPathNotFound = errors.New("path not found")
)

// 上传大小上限（与前端压缩约定一致：图片 5MB，其余 20MB）。
const (
	MaxImageSizeBytes = 5 << 20  // 5MB
	MaxOtherSizeBytes = 20 << 20 // 20MB
	// MaxUploadBodyBytes 单次上传请求体上限（3 张 5MB 图片 + multipart 开销）。
	MaxUploadBodyBytes = 25 << 20 // 25MB
)

// ignoredDirNames 始终不展示的大目录（无论是否显示隐藏文件）。
// 这些目录体量大且对 AI 对话价值低，避免列目录拖慢接口 / 前端误展开。
var ignoredDirNames = map[string]bool{
	"node_modules": true,
	"dist":         true,
	"build":        true,
	"out":          true,
	".venv":        true,
	"venv":         true,
	"__pycache__":  true,
	".next":        true,
	".nuxt":        true,
	".turbo":       true,
	".cache":       true,
	"coverage":     true,
}

// isSkippedDirName 目录名是否应过滤（隐藏目录 / 忽略的大目录）。
// 工作区文件浏览与新建项目目录浏览共用同一过滤规则，保持一致行为。
func isSkippedDirName(name string) bool {
	return strings.HasPrefix(name, ".") || ignoredDirNames[name]
}

// FileService 提供工作区文件浏览与上传，以及新建项目弹窗的目录浏览。
type FileService struct {
	workspaceRepo *store.WorkspaceRepository
	// defaultCwd 是 session.default_cwd（配置的 Agent 默认工作目录），
	// 目录浏览接口 path 参数省略时的初始目录。
	defaultCwd string
}

// NewFileService 创建文件服务。
func NewFileService(workspaceRepo *store.WorkspaceRepository, defaultCwd string) *FileService {
	return &FileService{workspaceRepo: workspaceRepo, defaultCwd: defaultCwd}
}

// resolveInWorkspace 把相对路径 rel 安全解析为工作区内的绝对路径。
//
// 规则：
//  1. rel 为空 / "." 视为工作区根；
//  2. Clean 后若为绝对路径或以 `..` 开头 → 越界；
//  3. 拼接后 EvalSymlinks 求真实路径，必须仍在工作区真实路径内（防 symlink 逃逸）。
//
// 注意：目标路径必须已存在（EvalSymlinks 需要解析真实路径）；
// 上传场景目录必然已存在，故满足此前提。
func (s *FileService) resolveInWorkspace(ws *model.Workspace, rel string) (string, error) {
	root, err := filepath.Abs(ws.Path)
	if err != nil {
		return "", fmt.Errorf("resolve workspace path: %w", err)
	}
	// 工作区真实路径（解析符号链接后），作为边界基准
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve workspace root: %w", err)
	}

	clean := filepath.Clean(rel)
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", ErrPathOutsideWorkspace
	}

	joined := filepath.Join(root, clean)
	real, err := filepath.EvalSymlinks(joined)
	if err != nil {
		// 目标不存在（如上传目标目录被删除）→ 明确 404；其他错误按系统错误返回
		if errors.Is(err, os.ErrNotExist) {
			return "", ErrPathNotFound
		}
		return "", fmt.Errorf("resolve target path: %w", err)
	}
	// 最终真实路径必须落在工作区真实路径内（相等或为其子路径）
	if real != realRoot && !strings.HasPrefix(real, realRoot+string(filepath.Separator)) {
		return "", ErrPathOutsideWorkspace
	}
	return real, nil
}

// ListDir 列出工作区下 rel 目录内容（目录在前、按名称排序）。
// 隐藏文件（以 `.` 开头）与 ignoredDirNames 大目录由后端强制过滤，不提供开关。
func (s *FileService) ListDir(workspaceID uint, rel string) (*model.FileListDTO, error) {
	ws, err := s.workspaceRepo.GetByID(workspaceID)
	if err != nil {
		return nil, fmt.Errorf("load workspace: %w", err)
	}
	dir, err := s.resolveInWorkspace(ws, rel)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("stat directory: %w", err)
	}
	if !info.IsDir() {
		return nil, ErrNotDirectory
	}
	items, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read directory: %w", err)
	}

	// 相对路径统一用 `/` 分隔输出给前端（Windows 下 filepath.Join 是 `\`）
	relSlash := filepath.ToSlash(cleanRel(rel))
	// 空切片而非 nil：JSON 序列化为 [] 而不是 null，前端可直接 .length
	dto := &model.FileListDTO{Path: relSlash, Entries: []model.FileEntryDTO{}}
	for _, item := range items {
		name := item.Name()
		// 隐藏文件（dotfile）与忽略大目录一律不展示，避免敏感文件（.env 等）泄露到 UI
		if isSkippedDirName(name) {
			continue
		}
		entry := model.FileEntryDTO{
			Name:  name,
			IsDir: item.IsDir(),
			Path:  pathJoin(relSlash, name),
		}
		if !item.IsDir() {
			if fi, err := item.Info(); err == nil {
				entry.Size = fi.Size()
			}
			entry.MimeType = mime.TypeByExtension(strings.ToLower(filepath.Ext(name)))
		}
		dto.Entries = append(dto.Entries, entry)
	}
	sort.Slice(dto.Entries, func(i, j int) bool {
		a, b := dto.Entries[i], dto.Entries[j]
		if a.IsDir != b.IsDir {
			return a.IsDir // 目录在前
		}
		return a.Name < b.Name
	})
	return dto, nil
}

// ListDirectories 列出绝对路径 dir 下的子文件夹（新建项目弹窗目录浏览用）。
//
// 与 ListDir 的区别：ListDir 是「workspace 安全边界内」的相对路径浏览；
// 本方法是新建项目弹窗的「服务器任意目录」浏览入口，path 为绝对路径，
// 不绑定 workspace。path 为空时返回 defaultCwd 解析后的绝对路径作为初始目录。
// 只返回文件夹（IsDir），隐藏目录与 ignoredDirNames 大目录不展示（见 isSkippedDirName）。
//
// 安全注意：本端点可枚举服务器任意绝对路径下的子文件夹（当前无鉴权，仅本地/内网部署），
// 若未来引入认证或收紧 CORS，应与此端点一并处理。
func (s *FileService) ListDirectories(dir string) (*model.DirectoryListDTO, error) {
	if dir == "" {
		dir = s.defaultCwd
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolve directory path: %w", err)
	}

	info, err := os.Stat(abs)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrPathNotFound
		}
		return nil, fmt.Errorf("stat directory: %w", err)
	}
	if !info.IsDir() {
		return nil, ErrNotDirectory
	}
	items, err := os.ReadDir(abs)
	if err != nil {
		// 无权限（EACCES）等系统错误在此包装，handler 层映射为 permission_denied
		return nil, fmt.Errorf("read directory: %w", err)
	}

	// 根目录（/ 或 Windows 盘符根）无上级；用 parent == abs 判断（filepath.Dir("/") == "/"）
	parent := filepath.Dir(abs)
	if parent == abs {
		parent = ""
	}

	// 输出路径统一用 `/` 分隔（Windows 下 filepath 是 `\`），与 ListDir 一致；
	// 前端面包屑按 `/` 分段、创建项目时后端会再次 Abs，分隔符差异无影响。
	// Entries 用空切片而非 nil：JSON 序列化为 [] 而不是 null，前端可直接 .length。
	dto := &model.DirectoryListDTO{
		Path:    filepath.ToSlash(abs),
		Parent:  filepath.ToSlash(parent),
		Entries: []model.DirectoryEntryDTO{},
	}
	for _, item := range items {
		// 只列文件夹；隐藏目录与忽略大目录过滤
		if !item.IsDir() || isSkippedDirName(item.Name()) {
			continue
		}
		dto.Entries = append(dto.Entries, model.DirectoryEntryDTO{
			Name: item.Name(),
			Path: filepath.ToSlash(filepath.Join(abs, item.Name())),
		})
	}
	sort.Slice(dto.Entries, func(i, j int) bool {
		return dto.Entries[i].Name < dto.Entries[j].Name
	})
	return dto, nil
}

// UploadFile 待落盘的上传文件（handler 从 multipart 提取后传入）。
type UploadFile struct {
	Name     string
	MimeType string
	Size     int64
	Reader   io.Reader
}

// UploadFiles 把上传文件保存到工作区下 relDir 目录，返回落盘后的条目列表。
//
// 校验顺序：目录存在且在工作区内 → 文件名清洗 → 拒绝覆盖 → 大小分档 → 写入。
func (s *FileService) UploadFiles(workspaceID uint, relDir string, files []UploadFile) ([]model.FileEntryDTO, error) {
	ws, err := s.workspaceRepo.GetByID(workspaceID)
	if err != nil {
		return nil, fmt.Errorf("load workspace: %w", err)
	}
	dir, err := s.resolveInWorkspace(ws, relDir)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("stat upload dir: %w", err)
	}
	if !info.IsDir() {
		return nil, ErrNotDirectory
	}

	relSlash := filepath.ToSlash(cleanRel(relDir))
	results := make([]model.FileEntryDTO, 0, len(files))
	for _, f := range files {
		// 注意：Go 标准库 multipart.Part.FileName() 已强制 filepath.Base（RFC 7578 §4.2，
		// 专门防目录路径逃逸），因此这里拿到的 name 不可能含路径分隔符；
		// 此处仅兜底空名 / "." / ".." 的极端情况。
		name := f.Name
		if name == "" || name == "." || name == ".." {
			return nil, ErrInvalidFileName
		}
		// 大小分档：图片 5MB，其他 20MB（与前端压缩约定一致）
		limit := int64(MaxOtherSizeBytes)
		if strings.HasPrefix(f.MimeType, "image/") || isImageExt(name) {
			limit = MaxImageSizeBytes
		}
		if f.Size > limit {
			return nil, ErrFileTooLarge
		}

		dst := filepath.Join(dir, name)
		// O_CREATE|O_EXCL 原子创建：目标已存在即失败（拒绝覆盖，防误覆盖源码），
		// 同时避免「先 Stat 后 Create」的 TOCTOU 窗口
		out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			if errors.Is(err, os.ErrExist) {
				return nil, fmt.Errorf("%w: %s", ErrFileExists, name)
			}
			return nil, fmt.Errorf("create file %s: %w", name, err)
		}
		// LimitReader 兜底：multipart 声明的 Size 可能与实际不符，写入上限 = limit+1，
		// 超限即报错（该文件是新建的，删除半成品不留残留）
		n, copyErr := io.Copy(out, io.LimitReader(f.Reader, limit+1))
		closeErr := out.Close()
		if copyErr != nil {
			_ = os.Remove(dst)
			return nil, fmt.Errorf("write file %s: %w", name, copyErr)
		}
		if closeErr != nil {
			_ = os.Remove(dst)
			return nil, fmt.Errorf("close file %s: %w", name, closeErr)
		}
		if n > limit {
			_ = os.Remove(dst)
			return nil, fmt.Errorf("%w: %s", ErrFileTooLarge, name)
		}

		results = append(results, model.FileEntryDTO{
			Name:     name,
			Path:     pathJoin(relSlash, name),
			IsDir:    false,
			Size:     f.Size,
			MimeType: mime.TypeByExtension(strings.ToLower(filepath.Ext(name))),
		})
	}
	return results, nil
}

// ResolveFile 解析工作区内文件的真实绝对路径（供 raw 下载/预览）。
// 返回的路径已经过 symlink 逃逸校验，可安全交给静态文件服务。
func (s *FileService) ResolveFile(workspaceID uint, rel string) (string, error) {
	ws, err := s.workspaceRepo.GetByID(workspaceID)
	if err != nil {
		return "", fmt.Errorf("load workspace: %w", err)
	}
	path, err := s.resolveInWorkspace(ws, rel)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("stat file: %w", err)
	}
	if info.IsDir() {
		return "", ErrNotDirectory
	}
	return path, nil
}

// IsImageName 按扩展名判断是否图片（用于上传大小分档）。
func isImageExt(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".svg", ".avif", ".ico", ".tiff", ".heic", ".heif":
		return true
	}
	return false
}

// cleanRel 规范化相对路径（空 → ""，"." → ""）。
func cleanRel(rel string) string {
	rel = filepath.Clean(rel)
	if rel == "." || rel == "" || rel == string(filepath.Separator) {
		return ""
	}
	return rel
}

// pathJoin 以 `/` 连接相对路径片段。
func pathJoin(base, name string) string {
	if base == "" {
		return name
	}
	return base + "/" + name
}

