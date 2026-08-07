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
	"strings"
	"time"

	"github.com/helloxz/zacp/internal/model"
	"github.com/helloxz/zacp/internal/store"
)

const (
	gitProbeTimeout       = 3 * time.Second
	gitStatusTimeout      = 5 * time.Second
	gitStatusMaxBytes     = 4 << 20
	gitStatusMaxFileCount = 2000
)

var (
	// ErrGitStatusTimeout Git 状态命令超过时间上限。
	ErrGitStatusTimeout = errors.New("git status timed out")
	// ErrGitStatusFailed Git 状态命令执行失败。
	ErrGitStatusFailed = errors.New("git status failed")
)

// GitService 提供工作区内只读 Git 状态查询。
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
	info, err := os.Stat(workspacePath)
	if err != nil {
		return nil, fmt.Errorf("stat workspace: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%w: workspace is not a directory", ErrGitStatusFailed)
	}

	result := &model.GitStatusDTO{Files: make([]model.GitChangeDTO, 0)}
	gitPath, err := exec.LookPath("git")
	if err != nil {
		return result, nil
	}
	result.GitInstalled = true

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
			return nil, fmt.Errorf("%w: repository probe", ErrGitStatusTimeout)
		}
		// rev-parse 非零通常表示普通目录，不把它当成服务故障，避免 Git Tab 红错。
		return result, nil
	}
	probeLines := strings.Split(strings.TrimSpace(string(probeOutput)), "\n")
	if len(probeLines) < 2 || strings.TrimSpace(probeLines[0]) != "true" {
		return result, nil
	}
	repoRoot, err := filepath.Abs(strings.TrimSpace(probeLines[len(probeLines)-1]))
	if err != nil {
		return nil, fmt.Errorf("resolve git root: %w", err)
	}
	realWorkspacePath, err := filepath.EvalSymlinks(workspacePath)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace symlinks: %w", err)
	}
	realRepoRoot, err := filepath.EvalSymlinks(repoRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve git root symlinks: %w", err)
	}
	result.IsRepository = true

	statusCtx, cancel := context.WithTimeout(ctx, gitStatusTimeout)
	defer cancel()
	if err := readGitStatus(statusCtx, gitPath, workspacePath, realRepoRoot, realWorkspacePath, result); err != nil {
		return nil, err
	}
	return result, nil
}

func readGitStatus(ctx context.Context, gitPath, workspacePath, repoRoot, workspaceRoot string, result *model.GitStatusDTO) error {
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
		return fmt.Errorf("%w: create stdout pipe: %v", ErrGitStatusFailed, err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("%w: start command: %v", ErrGitStatusFailed, err)
	}

	parseErr := parseGitStatusStream(stdout, repoRoot, workspaceRoot, result)
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

func parseGitStatusStream(stdout io.ReadCloser, repoRoot, workspaceRoot string, result *model.GitStatusDTO) error {
	defer stdout.Close()
	reader := bufio.NewReaderSize(stdout, 32*1024)
	var consumed int
	for {
		record, err := readNULRecord(reader)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		consumed += len(record) + 1
		if consumed > gitStatusMaxBytes {
			result.Truncated = true
			_, _ = io.Copy(io.Discard, reader)
			return nil
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
				return err
			}
			consumed += len(originalRepoPath) + 1
			if consumed > gitStatusMaxBytes {
				result.Truncated = true
				_, _ = io.Copy(io.Discard, reader)
				return nil
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
		appendGitChange(result, x, y, path, originalPath)
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

func gitChangeStatus(x, y byte) string {
	if x == '?' && y == '?' {
		return "untracked"
	}
	if x == 'U' || y == 'U' {
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
