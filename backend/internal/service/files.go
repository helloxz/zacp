package service

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/helloxz/zacp/internal/model"
	"github.com/helloxz/zacp/internal/store"
)

// ---------------------------------------------------------------------------
// FileService：工作区文件浏览、上传与重命名
//
// 安全模型（重要不变量）：
// - 所有相对路径先 Clean，拒绝绝对路径与 `..` 逃逸；
// - 拼接后 EvalSymlinks 解析真实路径，最终必须仍落在工作区根之内，
//   防止通过符号链接把读写引到工作区外（路径穿越 / symlink 逃逸）。
// - 重命名保留源目录项语义：校验真实目标在工作区内，但 Rename 使用词法路径，
//   避免把工作区内的符号链接重命名成其指向的真实文件。

// 文件操作相关错误（handler 层映射为统一错误结构）。
var (
	// ErrPathOutsideWorkspace 相对路径越界（`..` / 绝对路径 / symlink 逃逸）。
	ErrPathOutsideWorkspace = errors.New("path escapes workspace")
	// ErrNotDirectory 目标不是目录。
	ErrNotDirectory = errors.New("target is not a directory")
	// ErrFileExists 目标文件已存在（拒绝覆盖，防误覆盖源码）。
	ErrFileExists = errors.New("file already exists")
	// ErrInvalidFileName 文件名非法（空、`.`、`..`、含路径分隔符或 NUL）。
	ErrInvalidFileName = errors.New("invalid file name")
	// ErrCannotRenameRoot 不允许重命名工作区根目录。
	ErrCannotRenameRoot = errors.New("cannot rename workspace root")
	// ErrCannotDeleteRoot 不允许删除工作区根目录。
	ErrCannotDeleteRoot = errors.New("cannot delete workspace root")
	// ErrInvalidPathSegments 路径含 `.` 或 `..` 段（删除接口严格拒绝，不做 Clean 放行）。
	ErrInvalidPathSegments = errors.New("path contains . or .. segments")
	// ErrCannotDeleteIgnoredDir 目标在受保护大目录（.git / node_modules 等）内，禁止删除。
	ErrCannotDeleteIgnoredDir = errors.New("cannot delete ignored directory")
	// ErrFileTooLarge 单文件超过大小上限（图片 5MB / 其他 20MB）。
	ErrFileTooLarge = errors.New("file too large")
	// ErrFileTooLargeForEdit 文件超过文本编辑器可编辑上限（2MB）。
	// 与 ErrFileTooLarge 分开：上传是 5MB/20MB 分档，编辑统一 2MB，消息文案不同。
	ErrFileTooLargeForEdit = errors.New("file too large for editing")
	// ErrBinaryFile 内容含 NUL 字节，判定为二进制文件，拒绝在文本编辑器打开。
	ErrBinaryFile = errors.New("binary file")
	// ErrInvalidEncoding 内容不是合法 UTF-8（如 GBK），拒绝读取/写入以免破坏文件。
	ErrInvalidEncoding = errors.New("invalid utf-8 encoding")
	// ErrFileModified 保存时 mtime 与打开时不符，文件已被其他端修改（乐观锁冲突）。
	ErrFileModified = errors.New("file modified by others")
	// ErrPathNotFound 目标路径不存在。
	ErrPathNotFound = errors.New("path not found")
)

// 上传大小上限（与前端压缩约定一致：图片 5MB，其余 20MB）。
const (
	MaxImageSizeBytes = 5 << 20  // 5MB
	MaxOtherSizeBytes = 20 << 20 // 20MB
	// MaxUploadBodyBytes 单次上传请求体上限（3 张 5MB 图片 + multipart 开销）。
	MaxUploadBodyBytes = 25 << 20 // 25MB
	// MaxEditableSizeBytes 编辑器可打开/写入的文件大小上限（2MB）。
	// 超过该上限的文件拒绝读/写：避免把超大文件整读进内存、拖垮前端编辑器渲染。
	MaxEditableSizeBytes = 2 << 20 // 2MB
)

// ignoredDirNames 始终不展示的大目录（无论是否显示隐藏文件）。
// 这些目录体量大且对 AI 对话价值低，避免列目录拖慢接口 / 前端误展开。
// 注意：`.git` 也在内——即使强制显示隐藏文件，也不把 .git/objects 等内部结构暴露到 UI。
var ignoredDirNames = map[string]bool{
	".git":         true,
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

// isHiddenName 是否为隐藏文件/目录（以 `.` 开头）。
func isHiddenName(name string) bool {
	return strings.HasPrefix(name, ".")
}

// isSkippedDirName 是否为始终忽略的大目录（见 ignoredDirNames）。
func isSkippedDirName(name string) bool {
	return ignoredDirNames[name]
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

// ListDir 列出工作区下 rel 目录的内容（目录在前、按名称排序）。
// 强制显示隐藏文件（.gitignore、.env 等代码编辑高频对象）；
// 仅忽略 ignoredDirNames 大目录（node_modules、.git 等）。
// 安全边界在 resolveInWorkspace（路径逃逸校验），显示隐藏文件不扩大攻击面。
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
// 只返回文件夹（IsDir），隐藏目录与 ignoredDirNames 大目录均不展示
// （选目录场景下 .git/.config 等无意义；文件面板 ListDir 已改为显示隐藏文件）。
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
		if !item.IsDir() || isHiddenName(item.Name()) || isSkippedDirName(item.Name()) {
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

// RenameFile 在工作区内重命名文件或目录，只允许修改 basename，不允许移动到其他目录。
//
// 源路径与目标父目录都经过真实路径边界校验；目标使用 Lstat 检查，避免覆盖已有条目。
// 使用词法源路径执行 os.Rename，确保符号链接条目本身被重命名，而不是其指向目标。
func (s *FileService) RenameFile(workspaceID uint, relPath, newName string) (*model.FileEntryDTO, error) {
	ws, err := s.workspaceRepo.GetByID(workspaceID)
	if err != nil {
		return nil, fmt.Errorf("load workspace: %w", err)
	}
	if err := validateFileName(newName); err != nil {
		return nil, err
	}

	root, err := filepath.Abs(ws.Path)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace path: %w", err)
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace root: %w", err)
	}

	cleanPath := filepath.Clean(relPath)
	if cleanPath == "." || cleanPath == "" {
		return nil, ErrCannotRenameRoot
	}
	if filepath.IsAbs(cleanPath) || cleanPath == ".." || strings.HasPrefix(cleanPath, ".."+string(filepath.Separator)) {
		return nil, ErrPathOutsideWorkspace
	}

	source := filepath.Join(root, cleanPath)
	sourceInfo, err := os.Lstat(source)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrPathNotFound
		}
		return nil, fmt.Errorf("stat source path: %w", err)
	}
	parent := filepath.Dir(source)
	parentReal, err := filepath.EvalSymlinks(parent)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrPathNotFound
		}
		return nil, fmt.Errorf("resolve source directory: %w", err)
	}
	if !pathWithin(parentReal, realRoot) {
		return nil, ErrPathOutsideWorkspace
	}
	sourceReal, err := filepath.EvalSymlinks(source)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrPathNotFound
		}
		return nil, fmt.Errorf("resolve source path: %w", err)
	}
	if !pathWithin(sourceReal, realRoot) {
		return nil, ErrPathOutsideWorkspace
	}

	oldName := filepath.Base(cleanPath)
	if oldName == newName {
		return fileEntryFromInfo(filepath.ToSlash(cleanRel(cleanPath)), sourceInfo), nil
	}

	destination := filepath.Join(parent, newName)
	if _, err := os.Lstat(destination); err == nil {
		return nil, fmt.Errorf("%w: %s", ErrFileExists, newName)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("check destination path: %w", err)
	}
	if err := os.Rename(source, destination); err != nil {
		return nil, fmt.Errorf("rename %s to %s: %w", cleanPath, newName, err)
	}

	info, err := os.Lstat(destination)
	if err != nil {
		return nil, fmt.Errorf("stat renamed path: %w", err)
	}
	parentRel := filepath.ToSlash(filepath.Dir(cleanPath))
	if parentRel == "." {
		parentRel = ""
	}
	return fileEntryFromInfo(pathJoin(parentRel, newName), info), nil
}

// DeleteFile 删除工作区内的文件或目录（目录递归删除）。
//
// 防御要点（删除不可逆，校验比浏览/重命名更严格）：
//  1. 原始路径段检查：任一段为 `.` 或 `..`（含 `\` 转义变体）直接拒绝，
//     不做 Clean 后放行——与浏览接口的「Clean 后校验」策略不同，杜绝
//     通过 `a/../b` 之类变相改写路径；
//  2. 拒绝根路径（空 / `/` / `.` / `..`）——删除工作区根是灾难操作；
//  3. 拒绝 ignoredDirNames 中的目录（.git / node_modules 等）任意层级——
//     UI 对其隐藏，删除接口也应保持一致边界，防止绕过 UI 直接删掉 .git；
//  4. 绝对路径 / Clean 后越界 → ErrPathOutsideWorkspace；
//  5. 条目与父目录 EvalSymlinks 后必须在工作区根内（symlink 逃逸防护），
//     但实际删除使用词法路径 os.RemoveAll：删除的是 symlink 条目本身，
//     RemoveAll 不跟随 symlink，不会误删工作区外的真实目标。
func (s *FileService) DeleteFile(workspaceID uint, relPath string) error {
	ws, err := s.workspaceRepo.GetByID(workspaceID)
	if err != nil {
		return fmt.Errorf("load workspace: %w", err)
	}
	root, err := filepath.Abs(ws.Path)
	if err != nil {
		return fmt.Errorf("resolve workspace path: %w", err)
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return fmt.Errorf("resolve workspace root: %w", err)
	}

	// 注意：不做 TrimSpace——文件名首尾空格是合法字符（ListFiles 原样返回），
	// 删除是精确操作，trim 会改变目标身份导致误删其它文件。
	rel := relPath
	// 1) 根路径拒绝（空 / `/` / `.` / `..` 各种写法）
	if rel == "" || rel == "/" || rel == "." || rel == ".." {
		return ErrCannotDeleteRoot
	}
	// 2) 原始路径段检查：`.` / `..` 段一律拒绝（`\` 先转 `/` 再拆，覆盖 Windows 风格输入）
	for _, seg := range strings.Split(strings.ReplaceAll(rel, "\\", "/"), "/") {
		if seg == "." || seg == ".." {
			return ErrInvalidPathSegments
		}
	}
	if filepath.IsAbs(rel) {
		return ErrPathOutsideWorkspace
	}
	clean := filepath.Clean(rel)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return ErrPathOutsideWorkspace
	}

	// 3) 受保护大目录（.git / node_modules 等）任意层级都拒绝删除
	for _, seg := range strings.Split(clean, string(filepath.Separator)) {
		if ignoredDirNames[seg] {
			return ErrCannotDeleteIgnoredDir
		}
	}

	source := filepath.Join(root, clean)
	// 确认存在（删除接口不返回条目信息，Lstat 仅做存在性检查）
	if _, err := os.Lstat(source); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrPathNotFound
		}
		return fmt.Errorf("stat source path: %w", err)
	}
	// 4) 父目录真实路径必须在工作区根内
	parent := filepath.Dir(source)
	parentReal, err := filepath.EvalSymlinks(parent)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrPathNotFound
		}
		return fmt.Errorf("resolve source directory: %w", err)
	}
	if !pathWithin(parentReal, realRoot) {
		return ErrPathOutsideWorkspace
	}
	// 5) 条目自身 real 校验：symlink 指向工作区外则拒绝。
	//    悬空 symlink（EvalSymlinks 报 ENOENT）放行——条目自身存在（前面
	//    Lstat 已验证）且其父目录已在工作区内，词法 RemoveAll 删除链接本身安全，
	//    否则用户无法清理断链。
	sourceReal, err := filepath.EvalSymlinks(source)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("resolve source path: %w", err)
		}
	} else if !pathWithin(sourceReal, realRoot) {
		return ErrPathOutsideWorkspace
	}

	// 词法路径执行删除：RemoveAll 对文件与目录通用，且不跟随 symlink
	if err := os.RemoveAll(source); err != nil {
		return fmt.Errorf("delete %s: %w", clean, err)
	}
	return nil
}

func validateFileName(name string) error {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\\`) || strings.IndexByte(name, 0) >= 0 {
		return ErrInvalidFileName
	}
	return nil
}

func pathWithin(path, root string) bool {
	return path == root || strings.HasPrefix(path, root+string(filepath.Separator))
}

func fileEntryFromInfo(relPath string, info os.FileInfo) *model.FileEntryDTO {
	entry := &model.FileEntryDTO{
		Name:  filepath.Base(relPath),
		Path:  relPath,
		IsDir: info.IsDir(),
	}
	if !info.IsDir() {
		entry.Size = info.Size()
		entry.MimeType = mime.TypeByExtension(strings.ToLower(filepath.Ext(info.Name())))
	}
	return entry
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

// ReadTextFile 读取工作区内文本文件内容（供编辑器打开）。
//
// 校验链：路径边界（resolveInWorkspace）→ 是文件 → ≤2MB → 非二进制（无 NUL）→ 合法 UTF-8。
// 返回内容与 mtime（毫秒）：mtime 供前端保存时回传做乐观锁比对（见 WriteTextFile）。
func (s *FileService) ReadTextFile(workspaceID uint, rel string) (*model.FileContentDTO, error) {
	ws, err := s.workspaceRepo.GetByID(workspaceID)
	if err != nil {
		return nil, fmt.Errorf("load workspace: %w", err)
	}
	path, err := s.resolveInWorkspace(ws, rel)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat file: %w", err)
	}
	if info.IsDir() {
		return nil, ErrNotDirectory
	}
	if info.Size() > MaxEditableSizeBytes {
		return nil, ErrFileTooLargeForEdit
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}
	// 二进制检测：含 NUL 字节的文件按二进制处理（图片、压缩包等在文本编辑器打开会乱码）。
	if bytes.IndexByte(data, 0) >= 0 {
		return nil, ErrBinaryFile
	}
	// 非 UTF-8（如 GBK）拒绝打开，避免编辑器按 UTF-8 解码后保存时把原编码内容破坏掉。
	if !utf8.Valid(data) {
		return nil, ErrInvalidEncoding
	}
	return &model.FileContentDTO{
		Path:        filepath.ToSlash(cleanRel(rel)),
		Content:     string(data),
		Size:        info.Size(),
		MtimeUnixMs: info.ModTime().UnixMilli(),
	}, nil
}

// WriteTextFile 把编辑后的文本内容写回工作区内文件。
//
// 校验链：路径边界 → 是文件 → 内容 ≤2MB → 合法 UTF-8 →（可选）mtime 乐观锁。
// 乐观锁：前端打开时记录 mtime、保存时回传；不一致说明文件已被他处修改，拒绝覆盖（409）。
// 写入用 os.WriteFile（O_TRUNC），已存在文件的权限位保持不变。
func (s *FileService) WriteTextFile(workspaceID uint, rel, content string, expectedMtime *int64) (*model.FileContentDTO, error) {
	ws, err := s.workspaceRepo.GetByID(workspaceID)
	if err != nil {
		return nil, fmt.Errorf("load workspace: %w", err)
	}
	path, err := s.resolveInWorkspace(ws, rel)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat file: %w", err)
	}
	if info.IsDir() {
		return nil, ErrNotDirectory
	}
	if int64(len(content)) > MaxEditableSizeBytes {
		return nil, ErrFileTooLargeForEdit
	}
	// 与读侧镜像校验：写入内容不得含 NUL（否则保存后文件会被判定为二进制而无法再打开），
	// 且必须是合法 UTF-8（UTF-8 校验放在 NUL 之后，NUL 本身是合法 UTF-8）。
	if bytes.IndexByte([]byte(content), 0) >= 0 {
		return nil, ErrBinaryFile
	}
	if !utf8.ValidString(content) {
		return nil, ErrInvalidEncoding
	}
	// mtime 乐观锁：仅在客户端显式回传期望值时校验（老客户端不带则不强制）。
	if expectedMtime != nil && info.ModTime().UnixMilli() != *expectedMtime {
		return nil, ErrFileModified
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return nil, fmt.Errorf("write file: %w", err)
	}
	newInfo, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat file after write: %w", err)
	}
	return &model.FileContentDTO{
		Path:        filepath.ToSlash(cleanRel(rel)),
		Content:     content,
		Size:        newInfo.Size(),
		MtimeUnixMs: newInfo.ModTime().UnixMilli(),
	}, nil
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
