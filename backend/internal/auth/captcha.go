package auth

import (
	"bytes"
	crand "crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"image"
	"image/color"
	"image/png"
	"math/big"
	mrand "math/rand/v2"
	"sync"
	"time"
)

// 验证码有效期 5 分钟，单次有效。
const (
	captchaTTL    = 5 * time.Minute
	captchaLength = 4
	captchaWidth  = 120
	captchaHeight = 40
)

// captchaEntry 单个验证码条目。
type captchaEntry struct {
	code   string
	expire time.Time
}

// CaptchaStore 内存验证码存储：map + Mutex，过期懒清理。
// 单机单进程足够，无需 Redis/DB。
type CaptchaStore struct {
	mu      sync.Mutex
	entries map[string]*captchaEntry
}

// NewCaptchaStore 创建验证码存储。
func NewCaptchaStore() *CaptchaStore {
	return &CaptchaStore{entries: make(map[string]*captchaEntry)}
}

// Generate 生成新验证码：返回 id 与 base64 图片（data URI 前缀由调用方拼接）。
func (s *CaptchaStore) Generate() (id string, b64Image string) {
	code := randomCaptchaCode(captchaLength)
	id = newTokenValue() // 复用 token 生成：32字节 hex
	imgB64 := renderCaptchaImage(code)

	s.mu.Lock()
	defer s.mu.Unlock()
	// 懒清理过期项（避免长期不访问时堆积）
	now := time.Now()
	for k, v := range s.entries {
		if now.After(v.expire) {
			delete(s.entries, k)
		}
	}
	s.entries[id] = &captchaEntry{code: code, expire: now.Add(captchaTTL)}
	return id, imgB64
}

// Verify 校验验证码：大小写不敏感，单次有效，过期无效。
// 无论成功失败，命中后立即删除（单次有效，防重放）。
func (s *CaptchaStore) Verify(id, code string) bool {
	if id == "" || code == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[id]
	if !ok {
		return false
	}
	// 单次有效：先删除
	delete(s.entries, id)
	if time.Now().After(e.expire) {
		return false
	}
	// 验证码仅数字，大小写不敏感无影响；保留通用比较
	return equalFold(e.code, code)
}

// equalFold 简单大小写不敏感比较（验证码仅数字字母）。
func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca := a[i]
		cb := b[i]
		if ca >= 'a' && ca <= 'z' {
			ca -= 'a' - 'A'
		}
		if cb >= 'a' && cb <= 'z' {
			cb -= 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}

// randomCaptchaCode 生成指定长度的字母数字验证码（去 0/O/1/I 混淆，32字符）。
// 后端 Verify 统一大小写不敏感（equalFold），前端输入大小写均可。
func randomCaptchaCode(n int) string {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, n)
	for i := range b {
		idx, _ := crand.Int(crand.Reader, big.NewInt(int64(len(alphabet))))
		b[i] = alphabet[idx.Int64()]
	}
	return string(b)
}

// charFont 5x7 点阵字体（数字+大写字母，去混淆后仍保留 I/O 字形以备兼容）。
var charFont = map[byte][7]string{
	'0': {"01110", "10001", "10011", "10101", "11001", "10001", "01110"},
	'1': {"00100", "01100", "00100", "00100", "00100", "00100", "01110"},
	'2': {"01110", "10001", "00001", "00010", "00100", "01000", "11111"},
	'3': {"11111", "00010", "00100", "00010", "00001", "10001", "01110"},
	'4': {"00010", "00110", "01010", "10010", "11111", "00010", "00010"},
	'5': {"11111", "10000", "11110", "00001", "00001", "10001", "01110"},
	'6': {"00110", "01000", "10000", "11110", "10001", "10001", "01110"},
	'7': {"11111", "00001", "00010", "00100", "01000", "01000", "01000"},
	'8': {"01110", "10001", "10001", "01110", "10001", "10001", "01110"},
	'9': {"01110", "10001", "10001", "01111", "00001", "00010", "01100"},
	'A': {"01110", "10001", "10001", "11111", "10001", "10001", "10001"},
	'B': {"11110", "10001", "10001", "11110", "10001", "10001", "11110"},
	'C': {"01110", "10001", "10000", "10000", "10000", "10001", "01110"},
	'D': {"11100", "10010", "10001", "10001", "10001", "10010", "11100"},
	'E': {"11111", "10000", "10000", "11110", "10000", "10000", "11111"},
	'F': {"11111", "10000", "10000", "11110", "10000", "10000", "10000"},
	'G': {"01110", "10001", "10000", "10111", "10001", "10001", "01110"},
	'H': {"10001", "10001", "10001", "11111", "10001", "10001", "10001"},
	'I': {"01110", "00100", "00100", "00100", "00100", "00100", "01110"},
	'J': {"00111", "00001", "00001", "00001", "10001", "10001", "01110"},
	'K': {"10001", "10010", "10100", "11000", "10100", "10010", "10001"},
	'L': {"10000", "10000", "10000", "10000", "10000", "10000", "11111"},
	'M': {"10001", "11011", "10101", "10101", "10001", "10001", "10001"},
	'N': {"10001", "11001", "10101", "10011", "10001", "10001", "10001"},
	'O': {"01110", "10001", "10001", "10001", "10001", "10001", "01110"},
	'P': {"11110", "10001", "10001", "11110", "10000", "10000", "10000"},
	'Q': {"01110", "10001", "10001", "10001", "10101", "10010", "01101"},
	'R': {"11110", "10001", "10001", "11110", "10100", "10010", "10001"},
	'S': {"01110", "10001", "10000", "01110", "00001", "10001", "01110"},
	'T': {"11111", "00100", "00100", "00100", "00100", "00100", "00100"},
	'U': {"10001", "10001", "10001", "10001", "10001", "10001", "01110"},
	'V': {"10001", "10001", "10001", "10001", "10001", "01010", "00100"},
	'W': {"10001", "10001", "10001", "10101", "10101", "11011", "10001"},
	'X': {"10001", "10001", "01010", "00100", "01010", "10001", "10001"},
	'Y': {"10001", "10001", "10001", "01010", "00100", "00100", "00100"},
	'Z': {"11111", "00001", "00010", "00100", "01000", "10000", "11111"},
}

// digitFont 保留别名以兼容旧引用（历史遗留，现统一用 charFont）。
var digitFont = charFont

// renderCaptchaImage 自绘验证码图片：白底 + 干扰线/点 + 点阵数字，返回 base64 PNG。
func renderCaptchaImage(code string) string {
	img := image.NewRGBA(image.Rect(0, 0, captchaWidth, captchaHeight))
	// 白底
	white := color.RGBA{255, 255, 255, 255}
	for y := range captchaHeight {
		for x := range captchaWidth {
			img.Set(x, y, white)
		}
	}
	// 干扰线
	for range 6 {
		c := color.RGBA{
			uint8(mrand.IntN(150) + 50),
			uint8(mrand.IntN(150) + 50),
			uint8(mrand.IntN(150) + 50),
			255,
		}
		x1 := mrand.IntN(captchaWidth)
		y1 := mrand.IntN(captchaHeight)
		x2 := mrand.IntN(captchaWidth)
		y2 := mrand.IntN(captchaHeight)
		drawLine(img, x1, y1, x2, y2, c)
	}
	// 噪点
	for range 80 {
		c := color.RGBA{uint8(mrand.IntN(255)), uint8(mrand.IntN(255)), uint8(mrand.IntN(255)), 255}
		x := mrand.IntN(captchaWidth)
		y := mrand.IntN(captchaHeight)
		img.Set(x, y, c)
	}
	// 绘制字符：4 字符均匀分布，每字符随机颜色/轻微偏移
	cellW := captchaWidth / len(code)
	for i, ch := range code {
		cc := color.RGBA{
			uint8(mrand.IntN(80)),
			uint8(mrand.IntN(80)),
			uint8(mrand.IntN(80)),
			255,
		}
		// 随机偏移，保证不越界
		ox := i*cellW + mrand.IntN(6) + 4
		oy := mrand.IntN(8) + 6
		drawDigit(img, byte(ch), ox, oy, 3, cc)
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

// drawDigit 按点阵绘制单个字符（数字/字母），scale 为放大倍数。
func drawDigit(img *image.RGBA, ch byte, ox, oy, scale int, c color.RGBA) {
	// 兼容小写输入：统一转大写查表
	if ch >= 'a' && ch <= 'z' {
		ch -= 'a' - 'A'
	}
	font, ok := charFont[ch]
	if !ok {
		return
	}
	for row := range 7 {
		line := font[row]
		for col := range 5 {
			if line[col] == '1' {
				// 放大 scale 倍
				for dy := range scale {
					for dx := range scale {
						x := ox + col*scale + dx
						y := oy + row*scale + dy
						if x >= 0 && x < captchaWidth && y >= 0 && y < captchaHeight {
							img.Set(x, y, c)
						}
					}
				}
			}
		}
	}
}

// drawLine Bresenham 直线。
func drawLine(img *image.RGBA, x0, y0, x1, y1 int, c color.RGBA) {
	dx := abs(x1 - x0)
	dy := abs(y1 - y0)
	sx := -1
	if x0 < x1 {
		sx = 1
	}
	sy := -1
	if y0 < y1 {
		sy = 1
	}
	err := dx - dy
	for {
		if x0 >= 0 && x0 < captchaWidth && y0 >= 0 && y0 < captchaHeight {
			img.Set(x0, y0, c)
		}
		if x0 == x1 && y0 == y1 {
			break
		}
		e2 := 2 * err
		if e2 > -dy {
			err -= dy
			x0 += sx
		}
		if e2 < dx {
			err += dx
			y0 += sy
		}
	}
}

func abs(a int) int {
	if a < 0 {
		return -a
	}
	return a
}

// 生成随机 hex ID（备用，若 token 冲突可退化）
func newCaptchaID() string {
	b := make([]byte, 16)
	_, _ = crand.Read(b)
	return hex.EncodeToString(b)
}
