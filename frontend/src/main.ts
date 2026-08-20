import { createApp } from 'vue'
import { createPinia } from 'pinia'
import { registerSW } from 'virtual:pwa-register'
import App from './App.vue'
import router from './router'
import { setupI18n } from './locales'
import { resolveInitialLocale } from './utils/locale'
import './styles/main.css'
// incremark markdown 渲染主题（CSS 变量 + .incremark-* 前缀类，不影响全局布局）
import '@incremark/theme/styles.css'

const app = createApp(App)
const pinia = createPinia()
const i18n = setupI18n()

// 与 resolveInitialLocale 对齐 document.lang，利于无障碍与浏览器翻译提示
document.documentElement.lang = resolveInitialLocale()

app.use(pinia)
app.use(router)
app.use(i18n)
app.mount('#app')

// 注册 Service Worker（PWA）：
// - registerType: 'autoUpdate'：检测到新版本时后台静默更新，下次启动用新版。
// - immediate: true：页面加载即注册，尽早接管预缓存，避免首次访问离线不可用。
// - 注册是异步的，不影响 Vue 首屏渲染；若浏览器不支持（非 HTTPS/localhost），
//   registerSW 内部容错、不会抛错阻塞应用。
registerSW({ immediate: true })
