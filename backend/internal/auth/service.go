package auth

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/helloxz/zacp/internal/config"
)


// Service 单用户认证服务：持有启用状态、凭证与内存 token 存储。
//
// 设计要点：
//   - 启用/禁用由「password_hash 是否为空」决定，而非独立开关：
//     缺省（无 [auth] 段 / 空哈希）= 认证关闭，老用户零影响；
//   - 凭证变更（设置页改用户名/密码）热更新内存并原子写回 config.toml，无需重启；
//   - 变更后吊销全部已签发 token：现有登录态失效，需按新凭证重新登录；
//   - 验证码：内存 CaptchaStore，5 分钟过期单次有效；
//   - IP 拉黑：内存计数，5 次失败拉黑 24 小时，重启即清。
type Service struct {
	mu         sync.RWMutex
	enabled    bool
	username   string
	password   string // 存储的 password_hash（"sha256$<hex>"）
	configPath string // 写回目标：$ZACP_DATA/config.toml（或 ZACP_CONFIG 指定的文件）
	tokens     *TokenStore
	log        *slog.Logger
	// 验证码存储
	captchas *CaptchaStore
	// 登录失败 IP 维度拉黑
	attemptMu sync.Mutex
	attempts  map[string]*loginAttempt
}

// loginAttempt 记录单个 IP 的失败状态。
type loginAttempt struct {
	count        int
	blockedUntil time.Time
}

// 拉黑策略：5 次失败后封 24 小时（写死，按用户要求重启即清）。
const (
	loginBlockThreshold = 5
	loginBlockDuration  = 24 * time.Hour
)


// NewService 从配置初始化认证服务。
// configPath 为凭证写回目标；为空时 UpdateCredentials 会报错（理论上启动必经 config 加载，不会为空）。
func NewService(cfg *config.Config, configPath string, log *slog.Logger) *Service {
	s := &Service{
		configPath: configPath,
		tokens:     NewTokenStore(),
		captchas:   NewCaptchaStore(),
		attempts:   make(map[string]*loginAttempt),
		log:        log,
	}
	if cfg != nil {
		s.syncFromConfig(cfg)
	}
	return s
}


// syncFromConfig 将配置中的 [auth] 段同步到内存状态。
func (s *Service) syncFromConfig(cfg *config.Config) {
	s.enabled = cfg.Auth.PasswordHash != ""
	s.username = cfg.Auth.Username
	s.password = cfg.Auth.PasswordHash
}

// Enabled 认证是否启用（password_hash 非空）。
func (s *Service) Enabled() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.enabled
}

// Username 返回当前用户名（认证未启用时可能为空）。
func (s *Service) Username() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.username
}

// Login 校验用户名+密码；成功签发主 token（7 天）。
// 失败返回空 token（不区分「用户名错」还是「密码错」，由 handler 统一提示）。
func (s *Service) Login(username, password string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.enabled || username != s.username || !VerifyPassword(s.password, password) {
		return "", false
	}
	return s.tokens.IssueMain(username), true
}

// ValidateMain 校验主 token（Authorization Bearer 携带）。
func (s *Service) ValidateMain(token string) bool {
	if token == "" {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.enabled {
		return false // 未启用时中间件直接放行，正常不会走到这里
	}
	_, ok := s.tokens.ValidateMain(token)
	return ok
}

// IssueResourceToken 签发资源 token（文件直链，12 小时，绑定 workspace+path）。
func (s *Service) IssueResourceToken(workspace, path string) string {
	return s.tokens.IssueResource(workspace, path)
}

// ValidateResourceToken 校验资源 token 且绑定匹配。
func (s *Service) ValidateResourceToken(token, workspace, path string) bool {
	if token == "" {
		return false
	}
	return s.tokens.ValidateResource(token, workspace, path)
}

// UpdateCredentials 更新用户名与密码，并原子写回 config.toml：
//   - password 为空 → 清除密码、关闭认证（恢复默认「无需登录」）；
//   - password 非空 → 计算哈希、启用认证（此时 username 必填）。
//
// 成功后吊销全部已签发 token（现有登录态失效，前端应清理本地 token）。
func (s *Service) UpdateCredentials(username, password string) error {
	username = strings.TrimSpace(username)
	passwordHash := ""
	if password != "" {
		if username == "" {
			return fmt.Errorf("设置密码时必须填写用户名")
		}
		passwordHash = HashPassword(password)
	}
	if s.configPath == "" {
		return fmt.Errorf("无配置文件路径，无法写回认证设置")
	}
	if err := config.SetAuthCredentials(s.configPath, username, passwordHash); err != nil {
		return fmt.Errorf("写回配置失败: %w", err)
	}
	s.mu.Lock()
	s.username = username
	s.password = passwordHash
	s.enabled = passwordHash != ""
	s.tokens.RevokeAll()
	s.mu.Unlock()
	if s.log != nil {
		s.log.Info("auth credentials updated",
			"enabled", s.enabled, "username", username)
	}
	return nil
}

// GenerateCaptcha 生成图形验证码（免认证，用于登录页）。
func (s *Service) GenerateCaptcha() (id string, b64Image string) {
	return s.captchas.Generate()
}

// VerifyCaptcha 校验验证码（单次有效）。
func (s *Service) VerifyCaptcha(id, code string) bool {
	return s.captchas.Verify(id, code)
}

// IsBlocked 判断 IP 是否处于 24h 拉黑期内。
func (s *Service) IsBlocked(ip string) bool {
	if ip == "" {
		return false
	}
	s.attemptMu.Lock()
	defer s.attemptMu.Unlock()
	at, ok := s.attempts[ip]
	if !ok {
		return false
	}
	if at.blockedUntil.IsZero() {
		return false
	}
	if time.Now().After(at.blockedUntil) {
		delete(s.attempts, ip)
		return false
	}
	return true
}

// RecordFailure 记录一次密码错误；5 次后拉黑 24 小时。
func (s *Service) RecordFailure(ip string) {
	if ip == "" {
		return
	}
	s.attemptMu.Lock()
	defer s.attemptMu.Unlock()
	now := time.Now()
	at, ok := s.attempts[ip]
	if !ok {
		at = &loginAttempt{}
		s.attempts[ip] = at
	}
	if !at.blockedUntil.IsZero() && now.Before(at.blockedUntil) {
		return
	}
	if !at.blockedUntil.IsZero() && now.After(at.blockedUntil) {
		at.count = 0
		at.blockedUntil = time.Time{}
	}
	at.count++
	if at.count >= loginBlockThreshold {
		at.blockedUntil = now.Add(loginBlockDuration)
		if s.log != nil {
			s.log.Warn("login ip blocked", "ip", ip, "until", at.blockedUntil)
		}
	}
	for k, v := range s.attempts {
		if !v.blockedUntil.IsZero() && now.After(v.blockedUntil) {
			delete(s.attempts, k)
		}
	}
}

// RecordSuccess 登录成功后清除该 IP 的失败计数。
func (s *Service) RecordSuccess(ip string) {
	if ip == "" {
		return
	}
	s.attemptMu.Lock()
	defer s.attemptMu.Unlock()
	delete(s.attempts, ip)
}


