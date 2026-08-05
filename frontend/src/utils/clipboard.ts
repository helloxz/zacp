/**
 * 剪贴板工具。
 *
 * 本地开发环境（局域网 http、非 secure context）没有 `navigator.clipboard`，
 * 需要回退到隐藏 textarea + `document.execCommand('copy')` 老方案
 * （已废弃但所有浏览器仍支持，且必须在用户手势触发的调用链里执行——右键菜单正好满足）。
 * 封装成本函数后，任何地方复制文本都直接调 copyText，无需关心环境。
 */

/**
 * 复制文本到剪贴板。
 * @returns 是否复制成功（失败时调用方可提示用户手动复制）
 */
export async function copyText(text: string): Promise<boolean> {
  // 首选 Clipboard API（仅 secure context：https / localhost）
  if (navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(text)
      return true
    } catch {
      // 权限被拒等场景 → 走下方回退方案
    }
  }

  // 回退：隐藏 textarea + execCommand（非 secure context 可用）
  try {
    const ta = document.createElement('textarea')
    ta.value = text
    // 移出视口且不可见，避免页面跳动与闪现
    ta.style.position = 'fixed'
    ta.style.top = '0'
    ta.style.left = '-9999px'
    ta.style.opacity = '0'
    document.body.appendChild(ta)
    ta.focus()
    ta.select()
    const ok = document.execCommand('copy')
    ta.remove()
    return ok
  } catch {
    return false
  }
}
