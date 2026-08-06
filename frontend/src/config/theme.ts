import type { GlobalThemeOverrides } from 'naive-ui'

/**
 * 全局主题色（浅色）：清爽 sky 蓝，替换 Naive UI 默认的绿色主色。
 * 色值与 main.css 中 @theme 注册的 --color-primary 系列保持一致，
 * 保证 Naive 组件与 Tailwind 手写样式视觉统一。
 */
export const themeOverrides: GlobalThemeOverrides = {
  common: {
    primaryColor: '#0ea5e9', // sky-500
    primaryColorHover: '#38bdf8', // sky-400
    primaryColorPressed: '#0284c7', // sky-600
    primaryColorSuppl: '#0ea5e9',
  },
}

/**
 * 全局主题色（暗色）：与 darkTheme 搭配使用（App.vue 按 isDark 切换）。
 * 暗色深底上 sky-500 对比度不足，主色整体提亮一档（sky-400 起）；
 * 其余 common 变量（bodyColor/cardColor/textColor 等）跟随 Naive darkTheme 默认。
 */
export const darkThemeOverrides: GlobalThemeOverrides = {
  common: {
    primaryColor: '#38bdf8', // sky-400
    primaryColorHover: '#7dd3fc', // sky-300
    primaryColorPressed: '#0ea5e9', // sky-500
    primaryColorSuppl: '#38bdf8',
  },
}
