import { storeToRefs } from 'pinia'
import { useI18n } from 'vue-i18n'
import type { AppLocale } from '@/types/locale'
import { useAppStore } from '@/stores/app'

/**
 * 统一切换应用语言：Pinia + vue-i18n + document.lang。
 * 页面与顶栏语言切换组件应走这里，避免漏同步。
 */
export function useLocaleSwitch() {
  const appStore = useAppStore()
  const { locale } = storeToRefs(appStore)
  const { locale: i18nLocale } = useI18n()

  function setLocale(next: AppLocale) {
    appStore.setLocale(next)
    // vue-i18n Composition API 下 locale 为 WritableComputedRef
    i18nLocale.value = next
    if (typeof document !== 'undefined') {
      document.documentElement.lang = next
    }
  }

  return {
    locale,
    setLocale,
  }
}
