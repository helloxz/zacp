import { createApp } from 'vue'
import { createPinia } from 'pinia'
import App from './App.vue'
import router from './router'
import { setupI18n } from './locales'
import { resolveInitialLocale } from './utils/locale'
import './styles/main.css'

const app = createApp(App)
const pinia = createPinia()
const i18n = setupI18n()

// 与 resolveInitialLocale 对齐 document.lang，利于无障碍与浏览器翻译提示
document.documentElement.lang = resolveInitialLocale()

app.use(pinia)
app.use(router)
app.use(i18n)
app.mount('#app')
