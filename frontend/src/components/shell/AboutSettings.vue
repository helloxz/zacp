<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { LogoGithub } from '@vicons/ionicons5'
import pkg from '../../../package.json'

const { t } = useI18n()

/**
 * 版本号单一来源：frontend/package.json 的 version（随前端产物打包）。
 * 不依赖后端 /api/v1/version——dev 模式下后端返回 "dev"，会导致显示 "vdev"。
 */
const appVersion = pkg.version

/** 外链地址：项目主页与作者 X（新窗口打开，noopener 防反向劫持） */
const GITHUB_URL = 'https://github.com/helloxz/zacp'
const X_URL = 'https://x.com/xiaozblog'
</script>

<template>
  <div class="flex flex-col items-center gap-6 pt-2 text-center">
    <!-- Logo + 名称 + 简介 -->
    <div class="flex flex-col items-center gap-3">
      <!-- 应用图标：使用 public/favicon.png（与站点 favicon 一致） -->
      <img
        src="/favicon.png"
        alt=""
        class="h-16 w-16 rounded-2xl object-contain shadow-lg shadow-indigo-200"
      />
      <div>
        <h3 class="text-lg font-semibold text-slate-800">
          {{ t('common.appName') }}
        </h3>
        <p class="mx-auto mt-1 max-w-xs text-sm leading-relaxed text-slate-500">
          {{ t('settings.about.intro') }}
        </p>
      </div>
    </div>

    <!-- 版本号 -->
    <span
      class="inline-flex items-center gap-1.5 rounded-full bg-slate-100 px-3 py-1 text-xs font-medium text-slate-500"
    >
      {{ t('settings.about.version') }} v{{ appVersion }}
    </span>

    <!-- GitHub / X 外链 -->
    <div class="flex items-center gap-3">
      <a
        :href="GITHUB_URL"
        target="_blank"
        rel="noopener noreferrer"
        class="flex items-center gap-2 rounded-lg border border-slate-200 bg-white px-4 py-2 text-sm font-medium text-slate-700 shadow-sm transition-colors hover:border-slate-300 hover:bg-slate-50"
      >
        <n-icon :size="16"><LogoGithub /></n-icon>
        GitHub
      </a>
      <a
        :href="X_URL"
        target="_blank"
        rel="noopener noreferrer"
        aria-label="X"
        class="flex items-center justify-center rounded-lg bg-black p-2.5 text-white transition-opacity hover:opacity-80"
      >
        <!-- X 官方 logo：@vicons/ionicons5 无 LogoX，内联 SVG 兜底（按钮仅保留图标） -->
        <svg
          viewBox="0 0 24 24"
          class="h-4 w-4 fill-current"
          aria-hidden="true"
        >
          <path
            d="M18.244 2.25h3.308l-7.227 8.26 8.502 11.24H16.17l-5.214-6.817L4.99 21.75H1.68l7.73-8.835L1.254 2.25H8.08l4.713 6.231 5.451-6.231Zm-1.161 17.52h1.833L7.084 4.126H5.117l11.966 15.644Z"
          />
        </svg>
      </a>
    </div>
  </div>
</template>
