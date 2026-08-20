// 从源图标派生 PWA 所需各尺寸图标。
//
// 用法（仓库根目录）：
//
//	go run ./scripts/pwa-icons <源图> <输出目录>
//	例：go run ./scripts/pwa-icons ./ZacpAPP.jpg ./frontend/public/icons
//
// 输出的图标约定（与 vite.config.ts 的 manifest 及 index.html 引用保持一致）：
//   - pwa-192x192.png         标准 any 图标
//   - pwa-512x512.png         标准 any 图标
//   - maskable-512x512.png    Android 自适应图标（同尺寸另做 maskable 安全边距）
//   - apple-touch-icon.png    iOS 主屏图标（180x180，不透明）
//
// 源图要求：建议 >=512x512、不透明、主体居中且四周留 >=10% 边距（本项目
// ZacpAPP.jpg 的边距实测约 12%~14%，符合 maskable 安全区）。
//
// 说明：本脚本用纯标准库实现（无第三方依赖），其中包含一个轻量的
// 双线性插值缩放，用于把源图缩到 192/180 等小尺寸时保留边缘平滑。
// 生成前会把「近白背景」清洗为纯白（JPEG 压缩会让纯色背景带上轻微噪色，
// 缩到小尺寸时可能露出不均匀底色，清洗后保证系统图标/状态栏底色干净）。
package main

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/jpeg" // 注册 JPEG 解码器
	"image/png"
	"os"
	"path/filepath"
)

// isNearWhite 判断像素是否属于「近白背景」：三通道都接近 255 即视为背景，
// 重写为纯白。阈值取 245，JPEG 噪色（约 240~251）会被覆盖为纯白。
func isNearWhite(c color.Color) bool {
	r, g, b, _ := c.RGBA()
	rr, gg, bb := r>>8, g>>8, b>>8
	return rr > 245 && gg > 245 && bb > 245
}

// cleanToOpaque 返回一张不透明(alpha=255)且近白背景被清洗为纯白的 RGBA 图。
// 同时移除原图可能存在的透明边（PNG 源时），保证 iOS/maskable 图标底色干净。
func cleanToOpaque(src image.Image) *image.RGBA {
	b := src.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			c := src.At(b.Min.X+x, b.Min.Y+y)
			if isNearWhite(c) {
				dst.SetRGBA(x, y, color.RGBA{0xff, 0xff, 0xff, 0xff})
			} else {
				// 非背景像素：去透明并保留颜色
				r, g, bl, _ := c.RGBA()
				dst.SetRGBA(x, y, color.RGBA{uint8(r >> 8), uint8(g >> 8), uint8(bl >> 8), 0xff})
			}
		}
	}
	return dst
}

// resizeBilinear 用双线性插值把 src 缩放到 w x h。纯标准库实现，
// 适合缩小的图标（512 -> 192/180 等）。
func resizeBilinear(src image.Image, w, h int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	sw, sh := float64(src.Bounds().Dx()), float64(src.Bounds().Dy())
	for y := 0; y < h; y++ {
		// 目标 y 映射到源 y（含半像素对齐，避免整体偏移）
		sy := (float64(y)+0.5)*sh/float64(h) - 0.5
		if sy < 0 {
			sy = 0
		}
		y0, y1 := int(sy), int(sy)+1
		if y1 >= src.Bounds().Dy() {
			y1 = src.Bounds().Dy() - 1
		}
		ty := sy - float64(y0)
		for x := 0; x < w; x++ {
			sx := (float64(x)+0.5)*sw/float64(w) - 0.5
			if sx < 0 {
				sx = 0
			}
			x0, x1 := int(sx), int(sx)+1
			if x1 >= src.Bounds().Dx() {
				x1 = src.Bounds().Dx() - 1
			}
			tx := sx - float64(x0)
			// 双线性加权四邻域
			var r, g, b float64
			for _, p := range [][2]int{{x0, y0}, {x1, y0}, {x0, y1}, {x1, y1}} {
				px, py := p[0], p[1]
				wx := (1 - tx)
				if px == x1 {
					wx = tx
				}
				wy := (1 - ty)
				if py == y1 {
					wy = ty
				}
				wgt := wx * wy
				cr, cg, cb, _ := src.At(px, py).RGBA()
				r += float64(cr>>8) * wgt
				g += float64(cg>>8) * wgt
				b += float64(cb>>8) * wgt
			}
			dst.SetRGBA(x, y, color.RGBA{uint8(r), uint8(g), uint8(b), 0xff})
		}
	}
	return dst
}

// addMaskablePadding 在图标四周塞入安全边距（占总宽 padRatio 比例），
// 让主体落在 maskable 安全区（中心 80% 圆）内。源图标如果已有足够边距，
// 此步仅做轻微兜底，避免各平台剪裁裁到主体。
func addMaskablePadding(src image.Image, padRatio float64) *image.RGBA {
	sb := src.Bounds()
	pad := int(float64(sb.Dx()) * padRatio)
	w, h := sb.Dx()+2*pad, sb.Dy()+2*pad
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(dst, dst.Bounds(), image.NewUniform(color.RGBA{0xff, 0xff, 0xff, 0xff}), image.Point{}, draw.Src)
	draw.Draw(dst, image.Rect(pad, pad, pad+sb.Dx(), pad+sb.Dy()), src, sb.Min, draw.Src)
	return dst
}

func savePNG(path string, img image.Image) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "用法: go run ./scripts/pwa-icons <源图> <输出目录>")
		os.Exit(2)
	}
	srcPath, outDir := os.Args[1], os.Args[2]
	f, err := os.Open(srcPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "无法打开源图:", err)
		os.Exit(1)
	}
	defer f.Close()
	src, _, err := image.Decode(f)
	if err != nil {
		fmt.Fprintln(os.Stderr, "解码源图失败:", err)
		os.Exit(1)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "创建输出目录失败:", err)
		os.Exit(1)
	}
	clean := cleanToOpaque(src)

	// 512 直接使用清洗后的原尺寸（源图本身 512x512）
	if err := savePNG(filepath.Join(outDir, "pwa-512x512.png"), clean); err != nil {
		fmt.Fprintln(os.Stderr, "写入 pwa-512x512.png:", err)
		os.Exit(1)
	}
	// 192
	if err := savePNG(filepath.Join(outDir, "pwa-192x192.png"), resizeBilinear(clean, 192, 192)); err != nil {
		fmt.Fprintln(os.Stderr, "写入 pwa-192x192.png:", err)
		os.Exit(1)
	}
	// maskable 512：额外加 15% padding 保证安全区，并缩回 512x512
	mask := resizeBilinear(addMaskablePadding(clean, 0.15), 512, 512)
	if err := savePNG(filepath.Join(outDir, "maskable-512x512.png"), mask); err != nil {
		fmt.Fprintln(os.Stderr, "写入 maskable-512x512.png:", err)
		os.Exit(1)
	}
	// iOS apple-touch-icon：180x180，不透明（clean 已是纯白背景 + 不透明）
	if err := savePNG(filepath.Join(outDir, "apple-touch-icon.png"), resizeBilinear(clean, 180, 180)); err != nil {
		fmt.Fprintln(os.Stderr, "写入 apple-touch-icon.png:", err)
		os.Exit(1)
	}
	fmt.Println("已生成图标到", outDir)
}
