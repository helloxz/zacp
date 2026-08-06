import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import type { NDateLocale, NLocale } from 'naive-ui'
import { dateEnUS, dateZhCN, enUS, zhCN } from 'naive-ui'
import type { AppLocale } from '@/types/locale'
import { persistLocale, resolveInitialLocale } from '@/utils/locale'

/** 本地存储 key：用户显示名（仅本机） */
const DISPLAY_NAME_KEY = 'zacp.displayName'
/** 未设置显示名时的默认值（人名不做 i18n） */
const DEFAULT_DISPLAY_NAME = 'User'

/** 本地存储 key：主题模式（仅本机；与 index.html 防闪白脚本保持一致） */
const THEME_MODE_KEY = 'zacp.themeMode'
/** 主题模式：仅两态（浅色/深色），默认浅色保持现状 */
export type ThemeMode = 'light' | 'dark'

function readStoredDisplayName(): string {
  if (typeof localStorage === 'undefined') {
    return DEFAULT_DISPLAY_NAME
  }
  const stored = localStorage.getItem(DISPLAY_NAME_KEY)?.trim()
  return stored || DEFAULT_DISPLAY_NAME
}

function readStoredThemeMode(): ThemeMode {
  if (typeof localStorage === 'undefined') {
    return 'light'
  }
  return localStorage.getItem(THEME_MODE_KEY) === 'dark' ? 'dark' : 'light'
}

/**
 * 把主题副作用同步到 document：
 * - `.dark` class：驱动 Tailwind 的 dark: variant 与语义色 token 的暗色覆盖；
 * - `data-theme` 属性：驱动 incremark markdown 渲染的暗色样式（[data-theme="dark"]）。
 */
function applyThemeClass(mode: ThemeMode) {
  const root = document.documentElement
  root.classList.toggle('dark', mode === 'dark')
  root.setAttribute('data-theme', mode)
}

/**
 * 应用级 UI 状态：语言、显示名等。
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

  /** 用户显示名（左下用户区 / 设置抽屉共用） */
  const displayName = ref(readStoredDisplayName())

  /** 设置显示名；空值回退默认，并持久化到 localStorage */
  function setDisplayName(name: string) {
    const next = name.trim() || DEFAULT_DISPLAY_NAME
    displayName.value = next
    if (typeof localStorage !== 'undefined') {
      localStorage.setItem(DISPLAY_NAME_KEY, next)
    }
  }

  /** 主题模式：仅两态（浅色/深色），持久化到 localStorage */
  const themeMode = ref<ThemeMode>(readStoredThemeMode())

  const isDark = computed(() => themeMode.value === 'dark')

  // 保持 document 状态与 store 一致（index.html 防闪白脚本已处理首帧，这里兜底）
  applyThemeClass(themeMode.value)

  /** 浅色/深色互切；切完立即同步到 document（Tailwind .dark + incremark data-theme） */
  function toggleTheme() {
    themeMode.value = isDark.value ? 'light' : 'dark'
    if (typeof localStorage !== 'undefined') {
      localStorage.setItem(THEME_MODE_KEY, themeMode.value)
    }
    applyThemeClass(themeMode.value)
  }

  /** 新建项目弹窗开关（WelcomeHero 与 AppSidebar 共享） */
  const newProjectModalOpen = ref(false)

  return {
    locale,
    naiveLocale,
    naiveDateLocale,
    setLocale,
    displayName,
    setDisplayName,
    themeMode,
    isDark,
    toggleTheme,
    newProjectModalOpen,
  }
})
