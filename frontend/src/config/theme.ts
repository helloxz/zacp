import type { GlobalThemeOverrides } from 'naive-ui'

/**
 * 全局主题色：清爽 sky 蓝，替换 Naive UI 默认的绿色主色。
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
