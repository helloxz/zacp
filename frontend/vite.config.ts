import { fileURLToPath, URL } from 'node:url'
import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import { VitePWA } from 'vite-plugin-pwa'
import tailwindcss from '@tailwindcss/vite'
import AutoImport from 'unplugin-auto-import/vite'
import Components from 'unplugin-vue-components/vite'
import { NaiveUiResolver } from 'unplugin-vue-components/resolvers'

/**
 * Vite 配置。
 * Naive UI 按需引入（官方推荐）：
 * https://www.naiveui.com/zh-CN/os-theme/docs/import-on-demand
 * - unplugin-vue-components + NaiveUiResolver：模板中的 n-* 组件按需解析
 * - unplugin-auto-import：useMessage / useDialog 等组合式 API 按需注入
 * 组件请写在 template 里（n-button 等），不要在 script 里从 naive-ui 整包 import 组件。
 *
 * PWA（vite-plugin-pwa）：
 * - manifest：应用名 Zacp、主题色跟随系统明暗（浅 #0ea5e9 / 暗 #38bdf8）
 * - registerType: 'autoUpdate'：后台静默更新，下次启动用新版
 * - 离线策略 A：预缓存静态资源，能打开 UI 壳；实时 ACP 会话（WebSocket）本身
 *   需联网，离线无法继续，属聊天类产品常态。
 * - 图标源在 public/icons/（由 scripts/pwa-icons/main.go 从 ZacpAPP.jpg 派生）。
 */
export default defineConfig({
  base: '/',
  plugins: [
    vue(),
    tailwindcss(),
    AutoImport({
      imports: [
        'vue',
        'vue-router',
        'pinia',
        'vue-i18n',
        // Naive UI 组合式 API（文档中的按需写法）
        {
          'naive-ui': [
            'useDialog',
            'useMessage',
            'useNotification',
            'useLoadingBar',
          ],
        },
      ],
      dts: 'src/auto-imports.d.ts',
    }),
    Components({
      resolvers: [NaiveUiResolver()],
      dts: 'src/components.d.ts',
    }),
    VitePWA({
      registerType: 'autoUpdate',
      includeAssets: ['favicon.png'],
      manifest: {
        name: 'Zacp',
        short_name: 'Zacp',
        description: '多 Agent ACP 网关（Web UI 接入多种支持 ACP 协议的 Agent 工具）',
        lang: 'zh-CN',
        start_url: '/',
        scope: '/',
        display: 'standalone',
        orientation: 'any',
        theme_color: '#0ea5e9',
        background_color: '#ffffff',
        // 明暗两套主题色：系统深色时地址栏/状态栏用更亮的 sky-400
        // （theme_color 仅支持单值，此处以 manifest 内的 media 数组实现跟随，
        //  未列入 webmanifest 的额外 theme 通过 index.html 的 <meta name=theme-color media> 补充）
        icons: [
          { src: '/icons/pwa-192x192.png', sizes: '192x192', type: 'image/png' },
          { src: '/icons/pwa-512x512.png', sizes: '512x512', type: 'image/png' },
          {
            src: '/icons/maskable-512x512.png',
            sizes: '512x512',
            type: 'image/png',
            purpose: 'maskable',
          },
        ],
      },
      workbox: {
        // 离线策略 A：预缓存静态资源；Google Fonts 等外部 CDN 不加 runtime 缓存。
        globPatterns: ['**/*.{js,css,html,png,svg,ico,woff2,mp3}'],
        navigateFallback: '/index.html',
        // 构建产物已哈希，可安全长缓存（stale-while-revalidate 兜底网络）
        runtimeCaching: [
          {
            urlPattern: ({ url }) => url.pathname.startsWith('/icons/'),
            handler: 'CacheFirst',
            options: {
              cacheName: 'zacp-icons',
              expiration: { maxEntries: 20, maxAgeSeconds: 60 * 60 * 24 * 30 },
            },
          },
        ],
      },
      devOptions: {
        enabled: true,
        type: 'module',
      },
    }),
  ],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  server: {
    host: '0.0.0.0',
    port: 8681,
    strictPort: true,
    proxy: {
      // 开发期把 REST/WS 前缀转到后端（默认 8680，与 config.toml / cmd/server 默认一致）
      '/api': {
        target: 'http://127.0.0.1:8680',
        changeOrigin: true,
        ws: true,
      },
    },
  },
  preview: {
    host: '0.0.0.0',
    port: 8681,
    strictPort: true,
  },
})
