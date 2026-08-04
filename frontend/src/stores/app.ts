import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import type { NDateLocale, NLocale } from 'naive-ui'
import { dateEnUS, dateZhCN, enUS, zhCN } from 'naive-ui'
import type { AppLocale } from '@/types/locale'
import { persistLocale, resolveInitialLocale } from '@/utils/locale'

/**
 * 应用级 UI 状态：语言等。
 * 关键逻辑：vue-i18n 文案 + Naive ConfigProvider locale 必须同步切换。
 */
export const useAppStore = defineStore('app', () => {
  const locale = ref<AppLocale>(resolveInitialLocale())

  const naiveLocale = computed<NLocale>(() =>
    locale.value === 'zh-CN' ? zhCN : enUS,
  )

  const naiveDateLocale = computed<NDateLocale>(() =>
    locale.value === 'zh-CN' ? dateZhCN : dateEnUS,
  )

  /**
   * 切换语言并持久化；调用方需同步 i18n.global.locale。
   */
  function setLocale(next: AppLocale) {
    locale.value = next
    persistLocale(next)
  }

  return {
    locale,
    naiveLocale,
    naiveDateLocale,
    setLocale,
  }
})
