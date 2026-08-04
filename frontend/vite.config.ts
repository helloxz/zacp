import { fileURLToPath, URL } from 'node:url'
import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
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
 */
export default defineConfig({
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
