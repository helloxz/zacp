/** 应用支持的语言（中 / 英 / 越南语） */
export type AppLocale = 'zh-CN' | 'en-US' | 'vi-VN'

export const APP_LOCALES: readonly AppLocale[] = ['zh-CN', 'en-US', 'vi-VN'] as const

export const LOCALE_STORAGE_KEY = 'zacp.locale'
