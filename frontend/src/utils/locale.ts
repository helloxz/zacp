import type { AppLocale } from '@/types/locale'
import { APP_LOCALES, LOCALE_STORAGE_KEY } from '@/types/locale'

/** 是否为受支持的语言代码 */
export function isAppLocale(value: string | null | undefined): value is AppLocale {
  return !!value && (APP_LOCALES as readonly string[]).includes(value)
}

/**
 * 解析初始语言：
 * 1. localStorage 用户选择（优先生效）
 * 2. navigator.language（zh* → zh-CN，其余 → en-US）
 * 3. 兜底 zh-CN
 */
export function resolveInitialLocale(): AppLocale {
  if (typeof localStorage !== 'undefined') {
    const stored = localStorage.getItem(LOCALE_STORAGE_KEY)
    if (isAppLocale(stored)) {
      return stored
    }
  }

  if (typeof navigator !== 'undefined') {
    const lang = (navigator.language || '').toLowerCase()
    if (lang.startsWith('zh')) {
      return 'zh-CN'
    }
    // 非中文环境默认英文
    return 'en-US'
  }

  return 'zh-CN'
}

/** 持久化用户语言选择 */
export function persistLocale(locale: AppLocale): void {
  if (typeof localStorage === 'undefined') {
    return
  }
  localStorage.setItem(LOCALE_STORAGE_KEY, locale)
}
