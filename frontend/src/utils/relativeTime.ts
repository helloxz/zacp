import type { AppLocale } from '@/types/locale'

/** “昨天”在各语言下的写法，避免格式化函数里堆三元表达式 */
const YESTERDAY_TEXT: Record<AppLocale, string> = {
  'zh-CN': '昨天',
  'en-US': 'Yesterday',
  'vi-VN': 'Hôm qua',
}

/** “N 天前”：各语言语序不同，统一在此转换 */
function daysAgoText(days: number, locale: AppLocale): string {
  if (locale === 'zh-CN') {
    return `${days} 天前`
  }
  if (locale === 'vi-VN') {
    return `${days} ngày trước`
  }
  return `${days} days ago`
}

/**
 * 简易相对时间格式化（自写，不上重库）。
 * 规则：今天 → 显示时刻；昨天 → 昨天；N 天内 → N 天前；更早 → 日期。
 * @param iso ISO 8601 时间字符串（后端 time.Time JSON 序列化格式）
 */
export function formatRelativeTime(iso: string, locale: AppLocale): string {
  const date = new Date(iso)
  if (Number.isNaN(date.getTime())) {
    return ''
  }

  const now = new Date()
  const startOfToday = new Date(now.getFullYear(), now.getMonth(), now.getDate())
  const startOfDay = new Date(
    date.getFullYear(),
    date.getMonth(),
    date.getDate(),
  )
  const dayDiff = Math.round((startOfToday.getTime() - startOfDay.getTime()) / 86_400_000)

  if (dayDiff <= 0) {
    // 今天（含未来时钟偏差）：显示 HH:mm
    const hh = String(date.getHours()).padStart(2, '0')
    const mm = String(date.getMinutes()).padStart(2, '0')
    return `${hh}:${mm}`
  }
  if (dayDiff === 1) {
    return YESTERDAY_TEXT[locale]
  }
  if (dayDiff < 7) {
    return daysAgoText(dayDiff, locale)
  }
  // 更早：YYYY-MM-DD
  const y = date.getFullYear()
  const m = String(date.getMonth() + 1).padStart(2, '0')
  const d = String(date.getDate()).padStart(2, '0')
  return `${y}-${m}-${d}`
}
