/// <reference types="vite/client" />

/**
 * .vue 单文件组件模块声明。
 *
 * 新版 vite/client 不再自带 `*.vue` shim；普通 .ts 文件（如 router/index.ts）
 * 里动态 `import('@/pages/xxx.vue')` 时，TS 需要这里的全局模块声明才能解析类型。
 * 注意：该 shim 仅提供兜底类型，.vue 文件内部的精确类型仍由 vue-tsc/Volar 处理。
 */
declare module '*.vue' {
  import type { DefineComponent } from 'vue'
  // eslint-disable-next-line @typescript-eslint/no-empty-object-type
  const component: DefineComponent<{}, {}, unknown>
  export default component
}

interface ImportMetaEnv {
  /** 后端 HTTP 基础 URL；空表示同源 */
  readonly VITE_API_BASE_URL: string
}

interface ImportMeta {
  readonly env: ImportMetaEnv
}
