/// <reference types="vite/client" />

interface ImportMetaEnv {
  /** 后端 HTTP 基础 URL；空表示同源 */
  readonly VITE_API_BASE_URL: string
}

interface ImportMeta {
  readonly env: ImportMetaEnv
}
