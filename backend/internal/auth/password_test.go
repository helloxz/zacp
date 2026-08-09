package auth

import "testing"

func TestHashPasswordStableAndVerified(t *testing.T) {
	// 同一密码哈希稳定（固定盐 + SHA-256，无随机成分），且以 sha256$ 前缀标识算法版本
	h1 := HashPassword("secret123")
	h2 := HashPassword("secret123")
	if h1 != h2 {
		t.Fatalf("HashPassword 不稳定: %q != %q", h1, h2)
	}
	if len(h1) != len("sha256$")+64 {
		t.Fatalf("哈希格式异常: %q", h1)
	}
	if !VerifyPassword(h1, "secret123") {
		t.Fatal("正确密码应校验通过")
	}
	if VerifyPassword(h1, "wrong") {
		t.Fatal("错误密码不应通过")
	}
	if VerifyPassword(h1, "") {
		t.Fatal("空密码不应通过（业务层约定空哈希 = 关闭认证，不能与真实密码混淆）")
	}
}

func TestVerifyPasswordRejectsMalformedHash(t *testing.T) {
	if VerifyPassword("", "x") {
		t.Fatal("空哈希不应通过")
	}
	if VerifyPassword("plaintext", "x") {
		t.Fatal("无前缀哈希不应通过")
	}
}
