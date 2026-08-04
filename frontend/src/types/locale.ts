/** 应用支持的语言（中英双语） */
export type AppLocale = 'zh-CN' | 'en-US'

export const APP_LOCALES: readonly AppLocale[] = ['zh-CN', 'en-US'] as const

export const LOCALE_STORAGE_KEY = 'zacp.locale'
