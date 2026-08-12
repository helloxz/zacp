package service

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/helloxz/zacp/internal/model"
	"github.com/helloxz/zacp/internal/store"
)

const (
	gitProbeTimeout       = 3 * time.Second
	gitStatusTimeout      = 5 * time.Second
	gitStatusMaxBytes     = 4 << 20
	gitStatusMaxFileCount = 2000

	// gitWriteTimeout 写操作（add/commit/push）统一硬超时：慢仓库/网络挂起时兜底，
	// 避免 HTTP 请求与 goroutine 无限占用。push 无凭据时 GIT_TERMINAL_PROMPT=0
	// 会快速失败，通常不会真的等到超时。
	gitWriteTimeout = 60 * time.Second
	// gitCommitMessageMax 提交信息最大长度（rune 数）。
	gitCommitMessageMax = 200
	// gitCommitFilesMax 单次提交文件数上限（防御异常大的请求体）。
	gitCommitFilesMax = 1000
	// gitOutputMaxBytes 命令输出摘要截断长度：stderr 只透传尾部片段，
	// 避免超大输出刷屏，也防止错误信息里带出意外内容。
	gitOutputMaxBytes = 2 << 10
	// gitStagedHintMaxPaths 暂存区冲突提示最多列出的路径数。
	gitStagedHintMaxPaths = 20
)

var (
	// ErrGitStatusTimeout Git 状态命令超过时间上限。
	ErrGitStatusTimeout = errors.New("git status timed out")
	// ErrGitStatusFailed Git 状态命令执行失败。
	ErrGitStatusFailed = errors.New("git status failed")

	// ErrGitNotInstalled 未检测到 git 可执行文件。
	ErrGitNotInstalled = errors.New("git not installed")
	// ErrGitNotRepository 目标目录不在 git worktree 内。
	ErrGitNotRepository = errors.New("workspace is not a git repository")
	// ErrGitInvalidRequest 提交请求参数非法（路径越界/信息为空等，映射 400）。
	ErrGitInvalidRequest = errors.New("invalid git request")
	// ErrGitConflictedFiles 选中文件存在未解决的冲突（add 会强制标记 resolved，必须拒绝）。
	ErrGitConflictedFiles = errors.New("selected files have unresolved conflicts")
	// ErrGitStagedFilesNotSelected 暂存区存在未选中的已暂存文件，
	// 直接 commit 会把它们一并提交，违反「仅提交选中文件」语义。
	ErrGitStagedFilesNotSelected = errors.New("staged files not included in selection")
	// ErrGitWriteTimeout 写操作（add/commit/push）超过时间上限。
	ErrGitWriteTimeout = errors.New("git write operation timed out")
	// ErrGitCommitFailed git add / git commit 执行失败（stderr 摘要随错误透传）。
	ErrGitCommitFailed = errors.New("git commit failed")
	// ErrGitPushFailed git push 执行失败（stderr 摘要随错误透传）。
	ErrGitPushFailed = errors.New("git push failed")
)

// urlCredentialPattern 匹配 URL 中的 `scheme://user:pass@` 或 `scheme://user@`，
// 透传 git 输出前脱敏，避免把凭据带进错误信息/日志（如 https://token@host/...）。
var urlCredentialPattern = regexp.MustCompile(`://[^/\s@]+@`)

// GitService 提供工作区内 Git 状态查询与提交能力。
//
// 资源约束：命令有硬超时，stdout 使用流式 NUL 记录解析，返回条目和原始输出均有上限；
// 因此大型仓库不会把完整 git status 输出一次性读入 Go 堆内存。
type GitService struct {
	workspaceRepo *store.WorkspaceRepository
}

// NewGitService 创建 Git 状态服务。
func NewGitService(workspaceRepo *store.WorkspaceRepository) *GitService {
	return &GitService{workspaceRepo: workspaceRepo}
}

// Status 查询 workspace 所在 Git worktree 的状态。
// Git 未安装或 workspace 不在 worktree 内都属于正常状态，返回空文件列表而非错误。
func (s *GitService) Status(ctx context.Context, workspaceID uint) (*model.GitStatusDTO, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ws, err := s.workspaceRepo.GetByID(workspaceID)
	if err != nil {
		return nil, fmt.Errorf("load workspace: %w", err)
	}
	workspacePath, err := filepath.Abs(ws.Path)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace path: %w", err)
	}

	result := &model.GitStatusDTO{Files: make([]model.GitChangeDTO, 0)}
	gitPath, repoRoot, err := probeGitWorktree(ctx, workspacePath)
	if err != nil {
		return nil, err
	}
	result.GitInstalled = gitPath != ""
	if gitPath == "" || repoRoot == "" {
		return result, nil
	}
	result.IsRepository = true

	realWorkspacePath, err := filepath.EvalSymlinks(workspacePath)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace symlinks: %w", err)
	}
	statusCtx, cancel := context.WithTimeout(ctx, gitStatusTimeout)
	defer cancel()
	if err := readGitStatus(statusCtx, gitPath, workspacePath, repoRoot, realWorkspacePath, result); err != nil {
		return nil, err
	}
	return result, nil
}

// probeGitWorktree 探测 git 是否安装、workspace 是否位于 git worktree 内。
// 返回 git 可执行文件路径与解析过 symlink 的仓库根目录（绝对路径）。
// 未安装 → (gitPath="", repoRoot="", nil)；不在 worktree 内 → (gitPath, "", nil)；
// 探测超时 → ErrGitStatusTimeout；其余环境错误返回错误。
func probeGitWorktree(ctx context.Context, workspacePath string) (gitPath, repoRoot string, err error) {
	info, err := os.Stat(workspacePath)
	if err != nil {
		return "", "", fmt.Errorf("stat workspace: %w", err)
	}
	if !info.IsDir() {
		return "", "", fmt.Errorf("%w: workspace is not a directory", ErrGitStatusFailed)
	}

	gitPath, err = exec.LookPath("git")
	if err != nil {
		return "", "", nil
	}

	probeCtx, cancel := context.WithTimeout(ctx, gitProbeTimeout)
	defer cancel()
	probe := exec.CommandContext(
		probeCtx,
		gitPath,
		"-C", workspacePath,
		"rev-parse",
		"--is-inside-work-tree",
		"--show-toplevel",
	)
	probe.Stderr = io.Discard
	probe.Env = gitEnv()
	probeOutput, err := probe.Output()
	if err != nil {
		if probeCtx.Err() != nil {
			return "", "", fmt.Errorf("%w: repository probe", ErrGitStatusTimeout)
		}
		// rev-parse 非零通常表示普通目录，不把它当成服务故障，避免 Git Tab 红错。
		return gitPath, "", nil
	}
	probeLines := strings.Split(strings.TrimSpace(string(probeOutput)), "\n")
	if len(probeLines) < 2 || strings.TrimSpace(probeLines[0]) != "true" {
		return gitPath, "", nil
	}
	repoRoot, err = filepath.Abs(strings.TrimSpace(probeLines[len(probeLines)-1]))
	if err != nil {
		return "", "", fmt.Errorf("resolve git root: %w", err)
	}
	realRepoRoot, err := filepath.EvalSymlinks(repoRoot)
	if err != nil {
		return "", "", fmt.Errorf("resolve git root symlinks: %w", err)
	}
	return gitPath, realRepoRoot, nil
}

// runGitStatusCommand 启动 porcelain status 命令（工作区限定 `-- .`），
// 返回 stdout 管道与已启动的 cmd，供 readGitStatus / collectGitStatusEntries 共用。
func runGitStatusCommand(ctx context.Context, gitPath, workspacePath string) (io.ReadCloser, *exec.Cmd, error) {
	cmd := exec.CommandContext(
		ctx,
		gitPath,
		"-C", workspacePath,
		"--no-optional-locks",
		"status",
		"--porcelain=v1",
		"-z",
		"--untracked-files=all",
		"--",
		".",
	)
	cmd.Stderr = io.Discard
	cmd.Env = gitEnv()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("%w: create stdout pipe: %v", ErrGitStatusFailed, err)
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("%w: start command: %v", ErrGitStatusFailed, err)
	}
	return stdout, cmd, nil
}

func readGitStatus(ctx context.Context, gitPath, workspacePath, repoRoot, workspaceRoot string, result *model.GitStatusDTO) error {
	stdout, cmd, err := runGitStatusCommand(ctx, gitPath, workspacePath)
	if err != nil {
		return err
	}

	truncated, parseErr := parseGitStatusStream(stdout, repoRoot, workspaceRoot, gitStatusMaxBytes, func(path, originalPath string, x, y byte) {
		appendGitChange(result, x, y, path, originalPath)
	})
	if truncated {
		result.Truncated = true
	}
	waitErr := cmd.Wait()
	if parseErr != nil {
		return fmt.Errorf("%w: parse output: %v", ErrGitStatusFailed, parseErr)
	}
	if ctx.Err() != nil {
		return fmt.Errorf("%w", ErrGitStatusTimeout)
	}
	if waitErr != nil {
		return fmt.Errorf("%w: %v", ErrGitStatusFailed, waitErr)
	}
	return nil
}

// gitStatusCallback porcelain 条目回调；path/originalPath 已归一化为 workspace 相对路径（/ 分隔）。
type gitStatusCallback func(path, originalPath string, x, y byte)

// parseGitStatusStream 流式解析 `git status --porcelain=v1 -z` 输出并逐条回调。
// 累计读取超过 maxBytes 时丢弃剩余输出并返回 truncated=true（防止超大仓库占满内存）；
// 是否截断由调用方决定如何呈现（读状态 → 标记 Truncated；提交校验 → 拒绝）。
func parseGitStatusStream(stdout io.ReadCloser, repoRoot, workspaceRoot string, maxBytes int, cb gitStatusCallback) (bool, error) {
	defer stdout.Close()
	reader := bufio.NewReaderSize(stdout, 32*1024)
	var consumed int
	for {
		record, err := readNULRecord(reader)
		if err == io.EOF {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		consumed += len(record) + 1
		if consumed > maxBytes {
			_, _ = io.Copy(io.Discard, reader)
			return true, nil
		}
		if record == "" {
			continue
		}

		x, y, repoPath, ok := parseGitStatusRecord(record)
		if !ok {
			continue
		}
		originalRepoPath := ""
		if x == 'R' || x == 'C' || y == 'R' || y == 'C' {
			// porcelain -z 对重命名/复制输出 destination\0source；当前路径在前。
			originalRepoPath, err = readNULRecord(reader)
			if err != nil {
				return false, err
			}
			consumed += len(originalRepoPath) + 1
			if consumed > maxBytes {
				_, _ = io.Copy(io.Discard, reader)
				return true, nil
			}
		}

		path, ok := relativeGitPath(repoRoot, workspaceRoot, repoPath)
		if !ok {
			continue
		}
		originalPath := ""
		if originalRepoPath != "" {
			originalPath, ok = relativeGitPath(repoRoot, workspaceRoot, originalRepoPath)
			if !ok {
				continue
			}
		}
		cb(path, originalPath, x, y)
	}
}

func readNULRecord(reader *bufio.Reader) (string, error) {
	data, err := reader.ReadBytes(0)
	if len(data) > 0 && data[len(data)-1] == 0 {
		data = data[:len(data)-1]
	}
	if err != nil && len(data) == 0 {
		return "", err
	}
	return string(data), nil
}

func parseGitStatusRecord(record string) (byte, byte, string, bool) {
	if len(record) < 4 || record[2] != ' ' {
		return 0, 0, "", false
	}
	return record[0], record[1], record[3:], true
}

func relativeGitPath(repoRoot, workspaceRoot, repoPath string) (string, bool) {
	absolutePath := filepath.Join(repoRoot, filepath.FromSlash(repoPath))
	relativePath, err := filepath.Rel(workspaceRoot, absolutePath)
	if err != nil || relativePath == ".." || strings.HasPrefix(relativePath, ".."+string(filepath.Separator)) || filepath.IsAbs(relativePath) {
		return "", false
	}
	return filepath.ToSlash(relativePath), true
}

func appendGitChange(result *model.GitStatusDTO, x, y byte, path, originalPath string) {
	status := gitChangeStatus(x, y)
	result.Summary.Changed++
	if x != ' ' && x != '?' {
		result.Summary.Staged++
	}
	if y != ' ' && y != '?' {
		result.Summary.Unstaged++
	}
	if x == '?' && y == '?' {
		result.Summary.Untracked++
	}
	if status == "conflicted" {
		result.Summary.Conflicted++
	}

	if !visibleGitPath(path) || (originalPath != "" && !visibleGitPath(originalPath)) {
		result.HiddenCount++
		return
	}
	if len(result.Files) >= gitStatusMaxFileCount {
		result.Truncated = true
		return
	}
	result.Files = append(result.Files, model.GitChangeDTO{
		Path:           path,
		OriginalPath:   originalPath,
		Status:         status,
		IndexStatus:    string([]byte{x}),
		WorktreeStatus: string([]byte{y}),
	})
}

// gitIsConflicted 判断 porcelain v1 条目是否为 unmerged（冲突）。
// unmerged 全集共 7 种：DD AU UD UA DU AA UU；只判 U 会漏掉不含 U 的 AA/DD，
// 而 git add 对 unmerged 文件会强制标记 resolved——漏判等于单方面解决冲突。
func gitIsConflicted(x, y byte) bool {
	switch string([]byte{x, y}) {
	case "DD", "AU", "UD", "UA", "DU", "AA", "UU":
		return true
	}
	return false
}

func gitChangeStatus(x, y byte) string {
	if x == '?' && y == '?' {
		return "untracked"
	}
	if gitIsConflicted(x, y) {
		return "conflicted"
	}
	if x == 'R' || y == 'R' {
		return "renamed"
	}
	if x == 'C' || y == 'C' {
		return "copied"
	}
	for _, status := range []byte{x, y} {
		switch status {
		case 'A':
			return "added"
		case 'D':
			return "deleted"
		case 'M':
			return "modified"
		}
	}
	return "changed"
}

func visibleGitPath(path string) bool {
	path = strings.TrimPrefix(path, "./")
	for _, part := range strings.Split(path, "/") {
		if part != "" && isSkippedDirName(part) {
			return false
		}
	}
	return true
}

func gitEnv() []string {
	return append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_OPTIONAL_LOCKS=0",
	)
}

// loadWorkspacePath 按 ID 加载 workspace 并解析绝对路径（Commit/Push 共用的前置步骤）。
func (s *GitService) loadWorkspacePath(workspaceID uint) (string, error) {
	ws, err := s.workspaceRepo.GetByID(workspaceID)
	if err != nil {
		return "", fmt.Errorf("load workspace: %w", err)
	}
	workspacePath, err := filepath.Abs(ws.Path)
	if err != nil {
		return "", fmt.Errorf("resolve workspace path: %w", err)
	}
	return workspacePath, nil
}

// gitStatusEntry 一条 porcelain 变更（保留原始 index/worktree 状态位）。
type gitStatusEntry struct {
	indexStatus    byte
	worktreeStatus byte
	originalPath   string
}

// collectGitStatusEntries 运行 porcelain status 并返回工作区内全部条目的 map
// （key 为 / 分隔的 workspace 相对路径）。与 readGitStatus 不同：不做可见性过滤
// 与条数截断，供提交前的完整校验使用（隐藏路径也可能处于已暂存状态）。
func collectGitStatusEntries(ctx context.Context, gitPath, workspacePath, repoRoot, workspaceRoot string) (map[string]gitStatusEntry, error) {
	stdout, cmd, err := runGitStatusCommand(ctx, gitPath, workspacePath)
	if err != nil {
		return nil, err
	}

	entries := make(map[string]gitStatusEntry)
	truncated, parseErr := parseGitStatusStream(stdout, repoRoot, workspaceRoot, gitStatusMaxBytes, func(path, originalPath string, x, y byte) {
		entries[path] = gitStatusEntry{indexStatus: x, worktreeStatus: y, originalPath: originalPath}
	})
	waitErr := cmd.Wait()
	if parseErr != nil {
		return nil, fmt.Errorf("%w: parse output: %v", ErrGitStatusFailed, parseErr)
	}
	if ctx.Err() != nil {
		return nil, fmt.Errorf("%w", ErrGitStatusTimeout)
	}
	if waitErr != nil {
		return nil, fmt.Errorf("%w: %v", ErrGitStatusFailed, waitErr)
	}
	if truncated {
		// 变更数据超过解析上限：无法保证校验覆盖全部已暂存文件，宁可拒绝提交。
		return nil, fmt.Errorf("%w: 变更过多，无法安全提交（超过 %d 字节）", ErrGitInvalidRequest, gitStatusMaxBytes)
	}
	return entries, nil
}

// validateGitCommitFiles 校验并规范化提交文件列表。
// 拒绝：空列表、绝对路径、含 `..` 段、以分隔符开头的路径、重复项；返回 / 分隔的相对路径。
// 路径最终会原样传给 `git add -- <paths>`（不经 shell），校验保证其不会逃逸 workspace。
func validateGitCommitFiles(files []string) ([]string, error) {
	if len(files) == 0 {
		return nil, errors.New("请至少选择一个文件")
	}
	if len(files) > gitCommitFilesMax {
		return nil, fmt.Errorf("文件数量过多（最多 %d 个）", gitCommitFilesMax)
	}
	seen := make(map[string]struct{}, len(files))
	cleaned := make([]string, 0, len(files))
	for _, raw := range files {
		if raw == "" {
			return nil, errors.New("存在空路径")
		}
		if strings.ContainsAny(raw, "\x00\n\r") {
			return nil, fmt.Errorf("路径包含非法字符: %q", raw)
		}
		if filepath.IsAbs(raw) {
			return nil, fmt.Errorf("路径必须是相对路径: %q", raw)
		}
		rel := filepath.Clean(filepath.FromSlash(raw))
		if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("路径越界: %q", raw)
		}
		norm := filepath.ToSlash(rel)
		if _, dup := seen[norm]; dup {
			continue
		}
		seen[norm] = struct{}{}
		cleaned = append(cleaned, norm)
	}
	if len(cleaned) == 0 {
		return nil, errors.New("请至少选择一个文件")
	}
	return cleaned, nil
}

// Commit 提交选中的文件（可选 push），files 为相对 workspace 的路径。
//
// 安全与语义约束（与前端勾选行为对齐）：
//   - 每个文件必须确实存在对应变更（否则 400），防止提交幽灵路径；
//   - 禁止提交存在未解决冲突的文件（git add 会以工作区内容强制标记 resolved）；
//   - 暂存区不得存在「未选中的已暂存文件」——直接 commit 会连同它们一起提交，
//     违反「仅提交选中文件」；冲突条目除外（未解决冲突本来就会让 commit 失败）。
//
// push 失败不使整体请求失败：返回 Committed=true、Pushed=false + PushError 摘要，
// 由前端提示「已提交但推送失败」并提供重试（见 Push）。
func (s *GitService) Commit(ctx context.Context, workspaceID uint, message string, files []string, push bool) (*model.GitCommitResultDTO, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return nil, fmt.Errorf("%w: 提交信息不能为空", ErrGitInvalidRequest)
	}
	if utf8.RuneCountInString(message) > gitCommitMessageMax {
		return nil, fmt.Errorf("%w: 提交信息过长（最多 %d 个字符）", ErrGitInvalidRequest, gitCommitMessageMax)
	}
	cleanFiles, err := validateGitCommitFiles(files)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrGitInvalidRequest, err)
	}

	workspacePath, err := s.loadWorkspacePath(workspaceID)
	if err != nil {
		return nil, err
	}
	gitPath, repoRoot, err := probeGitWorktree(ctx, workspacePath)
	if err != nil {
		return nil, err
	}
	if gitPath == "" {
		return nil, fmt.Errorf("%w: 未检测到 Git", ErrGitNotInstalled)
	}
	if repoRoot == "" {
		return nil, fmt.Errorf("%w: 当前项目不是 Git 项目", ErrGitNotRepository)
	}
	realWorkspacePath, err := filepath.EvalSymlinks(workspacePath)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace symlinks: %w", err)
	}

	entries, err := collectGitStatusEntries(ctx, gitPath, workspacePath, repoRoot, realWorkspacePath)
	if err != nil {
		return nil, err
	}

	// 1) 选中文件必须真实存在变更；2) 冲突文件禁止 add。
	selected := make(map[string]struct{}, len(cleanFiles))
	for _, f := range cleanFiles {
		selected[f] = struct{}{}
		entry, ok := entries[f]
		if !ok {
			return nil, fmt.Errorf("%w: 文件没有变更或不在工作区内: %s", ErrGitInvalidRequest, f)
		}
		if gitIsConflicted(entry.indexStatus, entry.worktreeStatus) {
			return nil, fmt.Errorf("%w: 存在未解决的冲突，请先在 Git 中解决: %s", ErrGitConflictedFiles, f)
		}
	}

	// 3) 暂存区校验：已暂存（x 列非空格/问号）但未选中的文件会被本次 commit 一并提交，
	//    必须拒绝。冲突条目（unmerged）跳过：它们不会因 commit 其他文件而生效，
	//    且会让 git 本身拒绝提交（unmerged files），错误由 git 透传更准确。
	var stagedOthers []string
	for path, entry := range entries {
		if entry.indexStatus == ' ' || entry.indexStatus == '?' {
			continue
		}
		if gitIsConflicted(entry.indexStatus, entry.worktreeStatus) {
			continue
		}
		if _, ok := selected[path]; !ok {
			stagedOthers = append(stagedOthers, path)
		}
	}
	if len(stagedOthers) > 0 {
		sort.Strings(stagedOthers)
		listed := stagedOthers
		suffix := ""
		if len(listed) > gitStagedHintMaxPaths {
			listed = listed[:gitStagedHintMaxPaths]
			suffix = fmt.Sprintf(" 等 %d 个", len(stagedOthers))
		}
		return nil, fmt.Errorf(
			"%w: 暂存区包含未选中的文件，直接提交会连同它们一起提交，请先处理: %s%s",
			ErrGitStagedFilesNotSelected, strings.Join(listed, "、"), suffix,
		)
	}

	// 写操作使用独立于请求取消的 context（仅超时兜底）：HTTP 断连时若 kill 正在写
	// index 的 git 进程，可能残留 .git/index.lock 使该仓库后续操作全部失败；
	// 让写操作跑完（最多 60s）更安全，即使客户端已无法收到响应。
	writeCtx, cancel := context.WithTimeout(context.Background(), gitWriteTimeout)
	defer cancel()

	// 4) git add -- <paths>：-- 防止路径被当作选项解析；每个路径加 :(literal)
	//    前缀按字面匹配，避免文件名中的 glob 字符（*?[）被 git pathspec 展开
	//    而暂存到未选中的文件（违反「仅提交选中文件」语义）。
	addArgs := []string{"-C", workspacePath, "add", "--"}
	for _, f := range cleanFiles {
		addArgs = append(addArgs, ":(literal)"+f)
	}
	addCmd := exec.CommandContext(writeCtx, gitPath, addArgs...)
	addCmd.Env = gitEnv()
	if out, err := addCmd.CombinedOutput(); err != nil {
		if writeCtx.Err() != nil {
			return nil, fmt.Errorf("%w", ErrGitWriteTimeout)
		}
		// add 对多文件部分失败时会残留部分已暂存文件，提示用户可据此处理
		return nil, fmt.Errorf("%w: git add 失败（部分文件可能已暂存）: %s", ErrGitCommitFailed, gitOutputSummary(out))
	}

	// 5) git commit -m <message>：-m 单参数直传，不经 shell，无命令注入面。
	commitCmd := exec.CommandContext(writeCtx, gitPath, "-C", workspacePath, "commit", "-m", message)
	commitCmd.Env = gitEnv()
	if out, err := commitCmd.CombinedOutput(); err != nil {
		if writeCtx.Err() != nil {
			return nil, fmt.Errorf("%w", ErrGitWriteTimeout)
		}
		return nil, fmt.Errorf("%w: %s", ErrGitCommitFailed, gitOutputSummary(out))
	}

	result := &model.GitCommitResultDTO{Committed: true}
	// hash 供前端展示；失败不影响提交结果（罕见，仅记录）
	hashCmd := exec.CommandContext(writeCtx, gitPath, "-C", workspacePath, "rev-parse", "HEAD")
	hashCmd.Env = gitEnv()
	if hashOut, err := hashCmd.Output(); err == nil {
		result.CommitHash = strings.TrimSpace(string(hashOut))
	}

	if push {
		pushCmd := exec.CommandContext(writeCtx, gitPath, "-C", workspacePath, "push")
		pushCmd.Env = gitEnv()
		if out, err := pushCmd.CombinedOutput(); err != nil {
			if writeCtx.Err() != nil {
				result.PushError = "推送超时"
			} else {
				result.PushError = gitOutputSummary(out)
			}
			return result, nil
		}
		result.Pushed = true
	}
	return result, nil
}

// Push 推送当前分支（重试 commit+push 失败后已提交但未推送的内容）。
// 失败返回 ErrGitPushFailed（携带 git stderr 摘要）。
func (s *GitService) Push(ctx context.Context, workspaceID uint) (*model.GitPushResultDTO, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	workspacePath, err := s.loadWorkspacePath(workspaceID)
	if err != nil {
		return nil, err
	}
	gitPath, repoRoot, err := probeGitWorktree(ctx, workspacePath)
	if err != nil {
		return nil, err
	}
	if gitPath == "" {
		return nil, fmt.Errorf("%w: 未检测到 Git", ErrGitNotInstalled)
	}
	if repoRoot == "" {
		return nil, fmt.Errorf("%w: 当前项目不是 Git 项目", ErrGitNotRepository)
	}

	// 与 Commit 相同：独立于请求取消的 context，断连不 kill 写 index 的 git 进程。
	writeCtx, cancel := context.WithTimeout(context.Background(), gitWriteTimeout)
	defer cancel()
	pushCmd := exec.CommandContext(writeCtx, gitPath, "-C", workspacePath, "push")
	pushCmd.Env = gitEnv()
	if out, err := pushCmd.CombinedOutput(); err != nil {
		if writeCtx.Err() != nil {
			return nil, fmt.Errorf("%w", ErrGitWriteTimeout)
		}
		return nil, fmt.Errorf("%w: %s", ErrGitPushFailed, gitOutputSummary(out))
	}
	return &model.GitPushResultDTO{Pushed: true}, nil
}

// gitOutputSummary 截取命令输出末尾作为错误摘要（git 通常把原因放在最后），
// 并脱敏 URL 中的凭据，避免把 token 泄露进错误信息/日志。
func gitOutputSummary(out []byte) string {
	text := strings.TrimSpace(string(out))
	if text == "" {
		return "命令执行失败"
	}
	text = urlCredentialPattern.ReplaceAllString(text, "://***@")
	if len(text) > gitOutputMaxBytes {
		text = text[len(text)-gitOutputMaxBytes:]
	}
	return text
}
