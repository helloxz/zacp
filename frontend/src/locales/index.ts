import { createI18n } from 'vue-i18n'
import { resolveInitialLocale } from '@/utils/locale'
import enUS from './en-US'
import zhCN from './zh-CN'

export const messages = {
  'zh-CN': zhCN,
  'en-US': enUS,
}

/**
 * 创建 vue-i18n 实例。
 * legacy: false → Composition API（useI18n）；与 Vue 3 推荐用法一致。
 */
export function setupI18n() {
  const locale = resolveInitialLocale()

  return createI18n({
    legacy: false,
    globalInjection: true,
    locale,
    fallbackLocale: 'en-US',
    messages,
  })
}
