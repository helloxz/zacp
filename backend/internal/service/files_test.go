package service

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// newTestFileService 构造测试用 FileService（workspaceRepo 传 nil：目录浏览不依赖它）。
func newTestFileService(defaultCwd string) *FileService {
	return NewFileService(nil, defaultCwd)
}

// TestListDirectories_DefaultCwd 省略 path 时返回 defaultCwd 解析后的绝对路径。
func TestListDirectories_DefaultCwd(t *testing.T) {
	dir := t.TempDir()
	svc := newTestFileService(dir)
	got, err := svc.ListDirectories("")
	if err != nil {
		t.Fatalf("ListDirectories: %v", err)
	}
	if got.Path != filepath.ToSlash(dir) {
		t.Fatalf("Path = %q, want %q", got.Path, dir)
	}
}

// TestListDirectories_EmptyDir 空目录时 entries 为空切片（JSON 为 [] 而非 null）。
func TestListDirectories_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	svc := newTestFileService(dir)
	got, err := svc.ListDirectories(dir)
	if err != nil {
		t.Fatalf("ListDirectories: %v", err)
	}
	if got.Entries == nil {
		t.Fatal("Entries 为 nil，JSON 会序列化为 null，前端将崩溃")
	}
	if len(got.Entries) != 0 {
		t.Fatalf("空目录应无条目，got %d", len(got.Entries))
	}
	if got.Path != filepath.ToSlash(dir) {
		t.Fatalf("Path = %q, want %q", got.Path, dir)
	}
}

// TestListDirectories_Root 根目录的 parent 应为 ""（前端禁用「返回上级」）。
func TestListDirectories_Root(t *testing.T) {
	svc := newTestFileService(t.TempDir())
	got, err := svc.ListDirectories(string(filepath.Separator))
	if err != nil {
		t.Fatalf("ListDirectories(/): %v", err)
	}
	if got.Parent != "" {
		t.Fatalf("根目录 parent 应为空，got %q", got.Parent)
	}
	if got.Path != filepath.ToSlash(string(filepath.Separator)) {
		t.Fatalf("Path = %q, want %q", got.Path, string(filepath.Separator))
	}
}

// TestListDirectories_Filter 过滤规则：只列文件夹，隐藏目录与 ignoredDirNames 不出现，
// 且与 ListDir 的共享过滤函数 isSkippedDirName 行为一致。
func TestListDirectories_Filter(t *testing.T) {
	dir := t.TempDir()
	// 应展示
	mustMkdir(t, filepath.Join(dir, "alpha"))
	mustMkdir(t, filepath.Join(dir, "beta"))
	// 应过滤：隐藏目录
	mustMkdir(t, filepath.Join(dir, ".git"))
	// 应过滤：ignoredDirNames
	mustMkdir(t, filepath.Join(dir, "node_modules"))
	// 应过滤：普通文件（非目录）
	mustWriteFile(t, filepath.Join(dir, "plain.txt"), "x")

	svc := newTestFileService(dir)
	got, err := svc.ListDirectories(dir)
	if err != nil {
		t.Fatalf("ListDirectories: %v", err)
	}

	names := make([]string, 0, len(got.Entries))
	for _, e := range got.Entries {
		names = append(names, e.Name)
		if isSkippedDirName(e.Name) {
			t.Fatalf("返回了应过滤的目录 %q", e.Name)
		}
		if e.Path != filepath.ToSlash(filepath.Join(dir, e.Name)) {
			t.Fatalf("条目 %q 的 Path 不是绝对路径格式: %q", e.Name, e.Path)
		}
	}
	if !sort.StringsAreSorted(names) {
		t.Fatalf("条目应按名称排序: %v", names)
	}
	if len(names) != 2 || names[0] != "alpha" || names[1] != "beta" {
		t.Fatalf("期望 [alpha beta]，got %v", names)
	}
}

// TestListDirectories_NotDirectory 目标是普通文件时返回 ErrNotDirectory。
func TestListDirectories_NotDirectory(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a.txt")
	mustWriteFile(t, file, "x")

	svc := newTestFileService(dir)
	_, err := svc.ListDirectories(file)
	if !errors.Is(err, ErrNotDirectory) {
		t.Fatalf("期望 ErrNotDirectory，got %v", err)
	}
}

// TestListDirectories_PathNotFound 路径不存在返回 ErrPathNotFound。
func TestListDirectories_PathNotFound(t *testing.T) {
	svc := newTestFileService(t.TempDir())
	_, err := svc.ListDirectories(filepath.Join(t.TempDir(), "nope"))
	if !errors.Is(err, ErrPathNotFound) {
		t.Fatalf("期望 ErrPathNotFound，got %v", err)
	}
}

// TestListDirectories_PermissionDenied 无权限目录返回的错误可被 errors.Is(err, os.ErrPermission) 命中
// （handler 据此映射 403 permission_denied）。
func TestListDirectories_PermissionDenied(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root 不受目录权限限制，跳过")
	}
	dir := t.TempDir()
	locked := filepath.Join(dir, "locked")
	mustMkdir(t, locked)
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	svc := newTestFileService(dir)
	_, err := svc.ListDirectories(locked)
	if err == nil {
		t.Fatal("期望权限错误，got nil")
	}
	if !errors.Is(err, os.ErrPermission) {
		t.Fatalf("期望 errors.Is(os.ErrPermission) 命中，got %v", err)
	}
}

// --- helpers ---

func mustMkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
