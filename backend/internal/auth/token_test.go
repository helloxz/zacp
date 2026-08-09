package auth

import (
	"strings"
	"testing"
	"time"
)

func TestTokenStoreMainAndResource(t *testing.T) {
	s := NewTokenStore()

	main := s.IssueMain("alice")
	if len(main) != 64 || !isHex(main) {
		t.Fatalf("主 token 应为 64 位 hex，got %q", main)
	}
	if u, ok := s.ValidateMain(main); !ok || u != "alice" {
		t.Fatalf("主 token 校验应返回签发用户名，got %q/%v", u, ok)
	}

	res := s.IssueResource("1", "img/a.png")
	if u, ok := s.ValidateMain(res); ok {
		t.Fatalf("资源 token 不能当作主 token 使用（u=%q）", u)
	}
	if !s.ValidateResource(res, "1", "img/a.png") {
		t.Fatal("资源 token 绑定匹配时应校验通过")
	}
	// 绑定校验：workspace / path 任一不符均拒绝（防直链越权访问其它文件）
	if s.ValidateResource(res, "2", "img/a.png") {
		t.Fatal("workspace 不符应拒绝")
	}
	if s.ValidateResource(res, "1", "other.png") {
		t.Fatal("path 不符应拒绝")
	}
	if s.ValidateResource(res, "1", "img/a.png/") {
		t.Fatal("path 前缀变体应拒绝（严格字符串相等）")
	}
	// 资源 token 不能被主 token 校验接受
	if _, ok := s.ValidateMain(res); ok {
		t.Fatal("资源 token 不应通过主 token 校验")
	}
}

func TestTokenStoreExpiry(t *testing.T) {
	s := NewTokenStore()
	// 用极短 TTL 的注入方式验证过期路径：直接构造过期条目
	s.mu.Lock()
	s.entries["expired"] = &tokenEntry{
		kind:      TokenKindMain,
		username:  "alice",
		expiresAt: time.Now().Add(-time.Second),
	}
	s.mu.Unlock()

	if _, ok := s.ValidateMain("expired"); ok {
		t.Fatal("过期主 token 不应通过")
	}
	// 懒清理：过期条目应被移除
	s.mu.Lock()
	_, exists := s.entries["expired"]
	s.mu.Unlock()
	if exists {
		t.Fatal("过期条目应在校验后被懒清理")
	}
}

func TestTokenStoreRevokeAll(t *testing.T) {
	s := NewTokenStore()
	t1 := s.IssueMain("a")
	t2 := s.IssueMain("b")
	s.RevokeAll()
	if _, ok := s.ValidateMain(t1); ok {
		t.Fatal("RevokeAll 后旧 token 应失效")
	}
	if _, ok := s.ValidateMain(t2); ok {
		t.Fatal("RevokeAll 后旧 token 应失效")
	}
	if !s.ValidateResource(s.IssueResource("1", "p"), "1", "p") {
		t.Fatal("RevokeAll 后新签发的资源 token 应有效（签发在 RevokeAll 之后）")
	}
}

func TestTokenStoreIssueCleansExpired(t *testing.T) {
	s := NewTokenStore()
	s.mu.Lock()
	s.entries["stale"] = &tokenEntry{
		kind:      TokenKindResource,
		workspace: "1",
		path:      "p",
		expiresAt: time.Now().Add(-time.Minute),
	}
	s.mu.Unlock()

	s.IssueMain("new") // 签发路径应顺带清理过期项
	s.mu.Lock()
	_, exists := s.entries["stale"]
	s.mu.Unlock()
	if exists {
		t.Fatal("签发时应顺带清理过期条目")
	}
}

func isHex(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return false
		}
	}
	return true
}
