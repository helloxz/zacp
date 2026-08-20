package service

import (
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/helloxz/zacp/internal/model"
)

// ---------------------------------------------------------------------------
// 聊天输入框快捷键粘贴上传（工作区外临时目录）：
//
// 背景：聊天输入框 Ctrl/Cmd+V 粘贴上传的图片/其它文件默认写入系统临时目录
// （Linux 下 /tmp/{yyyyMMddHH}/xxx.webp，如 /tmp/2026081913/123.webp），
// 输入框自动填充 @绝对路径 供 agent 读取（ACP ReadTextFile 支持工作区外
// 绝对路径，见 acp/client ReadTextFile）。与工作区上传（UploadFiles）关键差异：
//   - 目标目录由本函数基于系统时间生成，**绝不接受客户端传路径**（任意文件写漏洞）；
//   - 同名文件直接覆盖（临时目录无源码风险，语义 = 后粘的覆盖先粘的）；
//   - 返回绝对路径而非工作区相对路径。
// 临时目录不做主动清理，交由系统 /tmp 回收机制（tmpfiles.d / tmpwatch 等）处理。
// ---------------------------------------------------------------------------

// UploadTempFiles 把上传文件写入系统临时目录 /tmp/{yyyyMMddHH}/ 下，
// 返回落盘文件的绝对路径列表。
// 安全不变量：
//   - 目录由后端生成（os.TempDir() + 当前小时），客户端只能传文件名；
//   - 文件名经 validateFileName 清洗（拒空名 / "." / ".." / 路径分隔符 / NUL），
//     再 filepath.Join 进时间目录，不可能逃逸出该目录；
//   - 大小分档统一 10MB（图片与其它文件一致，文件面板原图直传；输入框粘贴仍经前端 webp 压缩，此处为兜底）。
func (s *FileService) UploadTempFiles(files []UploadFile) ([]model.FileEntryDTO, error) {
	// 小时级时间目录：/tmp/2026081913（yyyyMMddHH，Go 参考格式 '2006010215'）。
	// 同小时内的同名文件落在同一目录 → 后粘的覆盖先粘的（覆盖语义按小时生效）。
	dir := filepath.Join(os.TempDir(), time.Now().Format("2006010215"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create temp upload dir: %w", err)
	}

	results := make([]model.FileEntryDTO, 0, len(files))
	for _, f := range files {
		// multipart.Part.FileName() 已强制 filepath.Base（RFC 7578 §4.2），
		// 这里复用清洗函数兜底空名 / "." / ".." / 残留分隔符。
		name := f.Name
		if err := validateFileName(name); err != nil {
			return nil, err
		}
		// 大小分档：统一 10MB（图片与其它文件一致）。
		limit := int64(MaxOtherSizeBytes)
		if strings.HasPrefix(f.MimeType, "image/") || isImageExt(name) {
			limit = MaxImageSizeBytes
		}
		if f.Size > limit {
			return nil, ErrFileTooLarge
		}

		// 同名覆盖：O_CREATE|O_TRUNC 直接覆盖（临时目录不担心误覆盖源码，
		// 与工作区上传的 O_EXCL 拒绝策略相反）。
		dst := filepath.Join(dir, name)
		out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
		if err != nil {
			return nil, fmt.Errorf("create temp file %s: %w", name, err)
		}
		// LimitReader 兜底：multipart 声明 Size 可能虚高，写入上限 = limit+1，
		// 超限即报错并删除半成品，不留残留。
		n, copyErr := io.Copy(out, io.LimitReader(f.Reader, limit+1))
		closeErr := out.Close()
		if copyErr != nil {
			_ = os.Remove(dst)
			return nil, fmt.Errorf("write temp file %s: %w", name, copyErr)
		}
		if closeErr != nil {
			_ = os.Remove(dst)
			return nil, fmt.Errorf("close temp file %s: %w", name, closeErr)
		}
		if n > limit {
			_ = os.Remove(dst)
			return nil, fmt.Errorf("%w: %s", ErrFileTooLarge, name)
		}

		results = append(results, model.FileEntryDTO{
			Name:     name,
			Path:     dst, // 绝对路径：前端直接填充 @/tmp/... 供 agent 读取
			IsDir:    false,
			Size:     f.Size,
			MimeType: mime.TypeByExtension(strings.ToLower(filepath.Ext(name))),
		})
	}
	return results, nil
}
