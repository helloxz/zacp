/**
 * fileUpload — 剪贴板粘贴提取 / 图片压缩 / 上传的公共逻辑。
 *
 * 由两处共用，行为必须保持一致：
 * - components/files/FileExplorer.vue：「文件」面板的拖拽/粘贴上传（多文件）；
 * - components/chat/Composer.vue：聊天输入框粘贴上传（仅图片、单张）。
 *
 * 若在此修改提取或压缩规则，两处同时生效。
 */

/** 图片压缩后大小上限（与后端 5MB 分档一致，保证后端不会拒绝） */
export const MAX_IMAGE_BYTES = 5 * 1024 * 1024

/** 单次上传操作的文件数量上限（粘贴与拖拽统一约束，超过拒绝整批） */
export const MAX_UPLOAD_FILES = 10

/**
 * 浏览器支持判定：仅 Chromium 系（Chrome/Edge）与 Firefox 支持从文件管理器
 * 粘贴文件；Safari 剪贴板不向网页暴露文件（平台限制，无解），归为不支持。
 */
export function isPasteSupported(): boolean {
  const ua = navigator.userAgent
  return /chrome|crios|edg\//i.test(ua) || /firefox/i.test(ua)
}

/** 焦点在可编辑元素（输入框/文本框/编辑器）内时不拦截，让文本粘贴正常工作 */
export function isEditableTarget(t: EventTarget | null): boolean {
  const el = t as HTMLElement | null
  if (!el || typeof el.closest !== 'function') return false
  return !!el.closest('input, textarea, [contenteditable="true"]')
}

/**
 * 从剪贴板提取可上传文件，两路来源：
 * 1. clipboardData.files —— 文件管理器（Finder/资源管理器）复制文件后粘贴，
 *    File 对象带原始文件名，可多选（Safari 此路为空，平台限制）。
 * 2. clipboardData.items —— 剪贴板图片（截图/复制网页图片），剪贴板只有像素
 *    数据、没有名称概念，需自动命名；剪贴板内容单一，只取第一个 image item。
 * 其余类型（text/uri-list、text/plain 等）一律忽略：file:// 链接无法读取内容。
 */
export function extractPastedFiles(e: ClipboardEvent): File[] {
  const files = Array.from(e.clipboardData?.files ?? [])
  if (files.length) return files

  const items = Array.from(e.clipboardData?.items ?? [])
  const item = items.find((i) => i.kind === 'file' && i.type.startsWith('image/'))
  const raw = item?.getAsFile()
  if (!raw) return []

  // 截图自动命名：时间戳精确到秒防重名，扩展名按实际 mime 取
  const ext = { 'image/jpeg': 'jpg', 'image/gif': 'gif', 'image/webp': 'webp' }[raw.type] ?? 'png'
  const d = new Date()
  const p = (n: number) => String(n).padStart(2, '0')
  const name = `pasted-${d.getFullYear()}${p(d.getMonth() + 1)}${p(d.getDate())}-${p(d.getHours())}${p(d.getMinutes())}${p(d.getSeconds())}.${ext}`
  return [new File([raw], name, { type: raw.type })]
}

/**
 * 图片转 webp（等比不裁剪）：原尺寸 0.8 → 超限降采样最长边 2048 → 降质量 0.6。
 * 非图片原样直传；压缩后仍超限也原样直传，由后端按大小规则处理。
 */
export async function prepareFile(file: File): Promise<File> {
  // 非图片原样直传；SVG 是矢量图且通常很小，直传保留矢量与无损性
  if (!file.type.startsWith('image/') || file.type === 'image/svg+xml') return file
  try {
    const img = await loadImage(file)
    const { naturalWidth: w, naturalHeight: h } = img
    let blob = await encodeWebp(img, w, h, 0.8)
    if (blob.size > MAX_IMAGE_BYTES) {
      const scale = Math.min(1, 2048 / Math.max(w, h))
      blob = await encodeWebp(
        img,
        Math.max(1, Math.round(w * scale)),
        Math.max(1, Math.round(h * scale)),
        0.8,
      )
    }
    if (blob.size > MAX_IMAGE_BYTES) {
      const scale = Math.min(1, 2048 / Math.max(w, h))
      blob = await encodeWebp(
        img,
        Math.max(1, Math.round(w * scale)),
        Math.max(1, Math.round(h * scale)),
        0.6,
      )
    }
    // 压缩后仍超限：原样直传，由后端按图片 5MB 规则处理
    const name = file.name.replace(/\.[^.]+$/, '') + '.webp'
    return new File([blob], name, { type: 'image/webp' })
  } catch {
    return file // 解码/编码失败（损坏文件、heic 等）→ 原样上传
  }
}

function loadImage(file: File): Promise<HTMLImageElement> {
  return new Promise((resolve, reject) => {
    const url = URL.createObjectURL(file)
    const img = new Image()
    img.onload = () => {
      URL.revokeObjectURL(url)
      resolve(img)
    }
    img.onerror = () => {
      URL.revokeObjectURL(url)
      reject(new Error('图片解码失败'))
    }
    img.src = url
  })
}

function encodeWebp(
  img: HTMLImageElement,
  w: number,
  h: number,
  q: number,
): Promise<Blob> {
  return new Promise((resolve, reject) => {
    const canvas = document.createElement('canvas')
    canvas.width = w
    canvas.height = h
    const ctx = canvas.getContext('2d')
    if (!ctx) {
      reject(new Error('canvas 不可用'))
      return
    }
    ctx.drawImage(img, 0, 0, w, h)
    canvas.toBlob(
      (b) => (b ? resolve(b) : reject(new Error('webp 编码失败'))),
      'image/webp',
      q,
    )
  })
}
