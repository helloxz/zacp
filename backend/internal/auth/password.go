// Package auth 提供单用户账号认证能力：密码哈希、内存 Token 存储与凭证管理。
// 定位为「基本安全」：SHA-256 + 固定盐（无 bcrypt 计算开销、零第三方依赖），
// token 存内存（重启即失效，需重新登录）。
package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
)

// 密码哈希方案前缀：将来升级算法（如换 PBKDF2/argon2）时通过前缀区分，兼容旧哈希。
const passwordScheme = "sha256$"

// passwordSalt 固定盐（用户指定的 "zacp"）。
// 单用户场景盐无需保密（盐的作用是防彩虹表预计算，不承担密钥职责）；
// 写死固定盐保证同一密码哈希稳定、实现零依赖。
const passwordSalt = "zacp"

// HashPassword 计算密码哈希：sha256(salt + password)，返回 "sha256$<hex>"。
// 空密码也能哈希，但业务层约定「password_hash 为空 = 认证关闭」，
// 由调用方保证不会对空密码执行本函数（见 Service.UpdateCredentials）。
func HashPassword(password string) string {
	sum := sha256.Sum256([]byte(passwordSalt + password))
	return passwordScheme + hex.EncodeToString(sum[:])
}

// VerifyPassword 校验密码与哈希是否匹配。
// 用常数时间比较，避免时序侧信道泄露「哈希是否相同」。
func VerifyPassword(hash, password string) bool {
	expected := HashPassword(password)
	return subtle.ConstantTimeCompare([]byte(hash), []byte(expected)) == 1
}
