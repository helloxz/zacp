<script setup lang="ts">
import { ref } from 'vue'
import { useDialog, useMessage } from 'naive-ui'
import { useI18n } from 'vue-i18n'
import { LogoGithub } from '@vicons/ionicons5'
import pkg from '../../../package.json'

const { t } = useI18n()
const message = useMessage()
const dialog = useDialog()

/**
 * 版本号单一来源：frontend/package.json 的 version（随前端产物打包）。
 * 不依赖后端 /api/v1/version——dev 模式下后端返回 "dev"，会导致显示 "vdev"。
 */
const appVersion = pkg.version

/** 外链地址：项目主页与作者 X（新窗口打开，noopener 防反向劫持） */
const GITHUB_URL = 'https://github.com/helloxz/zacp'
const X_URL = 'https://x.com/xiaozblog'

/** 问题反馈 / 更多产品 文字外链（新窗口打开，noopener 防反向劫持） */
const FEEDBACK_URL = 'https://github.com/helloxz/zacp/issues'
const PRODUCTS_URL = 'https://www.xphub.dev/'

/** 更新检测：仓库与 GitHub API 地址，与 update.sh 的 REPO / API_BASE 保持一致 */
const UPDATE_REPO = 'helloxz/zacp'
const UPDATE_API_BASE = 'https://api.github.com'
/** 更新说明文档：发现新版本时弹窗里的跳转目标 */
const CHANGELOG_URL = 'https://note.xiaoz.top/doc/zacp/note-397'
/** 检测超时（毫秒）：GitHub API 在部分网络下响应慢，超时按失败处理 */
const CHECK_TIMEOUT_MS = 10_000
/** 成功检测后的冷却（毫秒）：GitHub 未认证 API 有速率限制（60 次/时/IP），防连点触发限流 */
const COOLDOWN_MS = 30_000

const checking = ref(false)
/** 上次成功检测的时间戳，冷却期内点击只提示不请求 */
let lastCheckedAt = 0

/** GitHub Release 响应中仅需的字段 */
interface GitHubRelease {
  tag_name?: string
}

/**
 * 语义化版本比较（与 update.sh 的版本排序规则等价）：
 * 按 `.` 分段，每段先比数字前缀，数字相同再比剩余部分
 * （正式版 > 预发布后缀；后缀按数字感知比较，`-beta.2 < -beta.10`，与 sort -V 一致）。
 * 返回 -1 = a < b，0 = 相等，1 = a > b。
 */
function compareVersions(a: string, b: string): number {
  const pa = a.replace(/^v/, '').split('.')
  const pb = b.replace(/^v/, '').split('.')
  const len = Math.max(pa.length, pb.length)
  for (let i = 0; i < len; i++) {
    const sa = pa[i] ?? '0'
    const sb = pb[i] ?? '0'
    const ma = sa.match(/^\d+/)
    const mb = sb.match(/^\d+/)
    const na = ma ? parseInt(ma[0], 10) : 0
    const nb = mb ? parseInt(mb[0], 10) : 0
    if (na !== nb) return na < nb ? -1 : 1
    const cmp = compareSuffix(sa.slice(ma?.[0].length ?? 0), sb.slice(mb?.[0].length ?? 0))
    if (cmp !== 0) return cmp
  }
  return 0
}

/**
 * 数字前缀之后的剩余部分比较：
 * 空后缀（正式版，如 1.0.0）大于预发布后缀（如 1.0.0-beta.1）；
 * 非空后缀按数字感知切分逐段比较（-beta.2 < -beta.10，与 GNU sort -V 行为一致）。
 */
function compareSuffix(ra: string, rb: string): number {
  if (ra === rb) return 0
  if (ra === '') return 1
  if (rb === '') return -1
  // 切成 数字段 / 非数字段 交替序列（如 "-beta.10" -> ["-beta", ".", "10"]），逐段比较
  const pa = ra.match(/\d+|\D+/g) ?? []
  const pb = rb.match(/\d+|\D+/g) ?? []
  const len = Math.max(pa.length, pb.length)
  for (let i = 0; i < len; i++) {
    const xa = pa[i] ?? ''
    const xb = pb[i] ?? ''
    if (xa === xb) continue
    const na = /^\d+$/.test(xa) ? parseInt(xa, 10) : null
    const nb = /^\d+$/.test(xb) ? parseInt(xb, 10) : null
    if (na !== null && nb !== null) return na < nb ? -1 : 1
    return xa < xb ? -1 : 1
  }
  return 0
}

/**
 * 请求远程最新版本号（参考 update.sh resolve_latest 的检测逻辑）：
 * 优先 `/releases/latest`；该接口对仅含预发布的仓库返回 404，
 * 回退到 `releases?per_page=1` 取最新一条（含预发布）。
 * 带 AbortController 超时，失败时抛出异常由调用方提示用户。
 */
async function fetchLatestVersion(): Promise<string> {
  const fetchJson = async (url: string): Promise<unknown> => {
    const controller = new AbortController()
    const timer = setTimeout(() => controller.abort(), CHECK_TIMEOUT_MS)
    try {
      const res = await fetch(url, {
        headers: { Accept: 'application/vnd.github+json' },
        signal: controller.signal,
      })
      if (!res.ok) return null
      return await res.json()
    } finally {
      clearTimeout(timer)
    }
  }

  // 从 release 响应中提取 tag：/releases/latest 返回单个对象，/releases 返回数组
  const extractTag = (data: unknown): string | undefined => {
    if (!data || typeof data !== 'object') return undefined
    const item = Array.isArray(data) ? (data[0] as GitHubRelease | undefined) : (data as GitHubRelease)
    return item?.tag_name
  }

  let tag = extractTag(
    await fetchJson(`${UPDATE_API_BASE}/repos/${UPDATE_REPO}/releases/latest`),
  )
  if (!tag) {
    tag = extractTag(
      await fetchJson(`${UPDATE_API_BASE}/repos/${UPDATE_REPO}/releases?per_page=1`),
    )
  }
  if (!tag) {
    throw new Error('no tag_name in release response')
  }
  return tag.replace(/^v/, '')
}

/**
 * 检测更新入口：比较本地版本与远程最新版本。
 * - 本地 >= 远程：提示已是最新（与 update.sh 一致，不提示降级）；
 * - 本地 < 远程：弹窗提示，可跳转帮助文档查看更新说明；
 * - 请求失败：提示网络异常，不设置冷却，方便用户稍后重试。
 */
async function checkForUpdate() {
  if (checking.value) {
    return
  }
  if (Date.now() - lastCheckedAt < COOLDOWN_MS) {
    message.info(t('settings.about.cooldown'))
    return
  }

  checking.value = true
  try {
    const latest = await fetchLatestVersion()
    lastCheckedAt = Date.now()
    if (compareVersions(appVersion, latest) >= 0) {
      message.success(t('settings.about.latest', { version: `v${appVersion}` }))
      return
    }
    dialog.info({
      title: t('settings.about.updateTitle'),
      content: t('settings.about.updateContent', {
        current: `v${appVersion}`,
        latest: `v${latest}`,
      }),
      positiveText: t('settings.about.viewChangelog'),
      negativeText: t('settings.about.known'),
      // 用户点击「查看更新说明」：新窗口打开帮助文档（noopener 防反向劫持）
      onPositiveClick: () => {
        window.open(CHANGELOG_URL, '_blank', 'noopener noreferrer')
      },
    })
  } catch {
    message.error(t('settings.about.checkFailed'))
  } finally {
    checking.value = false
  }
}
</script>

<template>
  <div class="flex flex-col items-center gap-6 pt-2 text-center">
    <!-- Logo + 名称 + 简介 -->
    <div class="flex flex-col items-center gap-3">
      <!-- 应用图标：使用 public/favicon.png（与站点 favicon 一致） -->
      <img
        src="/favicon.png"
        alt=""
        class="h-16 w-16 rounded-2xl object-contain shadow-lg shadow-indigo-200 dark:shadow-indigo-900/40"
      />
      <div>
        <h3 class="text-lg font-semibold text-ink">
          {{ t('common.appName') }}
        </h3>
        <p class="mx-auto mt-1 max-w-xs text-sm leading-relaxed text-ink-muted">
          {{ t('settings.about.intro') }}
        </p>
      </div>
    </div>

    <!-- 版本号 + 检测更新按钮 -->
    <div class="flex items-center gap-2">
      <span
        class="inline-flex items-center gap-1.5 rounded-full bg-surface-hover px-3 py-1 text-xs font-medium text-ink-muted"
      >
        {{ t('settings.about.version') }} v{{ appVersion }}
      </span>
      <n-button
        size="tiny"
        secondary
        :loading="checking"
        :disabled="checking"
        @click="checkForUpdate"
      >
        {{ t('settings.about.checkUpdate') }}
      </n-button>
    </div>

    <!-- GitHub / X 外链 -->
    <div class="flex items-center gap-3">
      <a
        :href="GITHUB_URL"
        target="_blank"
        rel="noopener noreferrer"
        class="flex items-center gap-2 rounded-lg border border-divider bg-surface-raised px-4 py-2 text-sm font-medium text-ink-secondary shadow-sm transition-colors hover:border-divider hover:bg-surface-hover"
      >
        <n-icon :size="16"><LogoGithub /></n-icon>
        GitHub
      </a>
      <a
        :href="X_URL"
        target="_blank"
        rel="noopener noreferrer"
        aria-label="X"
        class="flex items-center justify-center rounded-lg bg-black p-2.5 text-white transition-opacity hover:opacity-80 dark:bg-slate-100 dark:text-slate-900"
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

    <!-- 问题反馈 / 更多产品：低调文字外链，与页面风格协调融合（新窗口打开） -->
    <div class="flex items-center gap-2 text-xs text-ink-muted">
      <a
        :href="FEEDBACK_URL"
        target="_blank"
        rel="noopener noreferrer"
        class="transition-colors hover:text-primary"
      >
        {{ t('settings.about.feedback') }}
      </a>
      <span aria-hidden="true">·</span>
      <a
        :href="PRODUCTS_URL"
        target="_blank"
        rel="noopener noreferrer"
        class="transition-colors hover:text-primary"
      >
        {{ t('settings.about.moreProducts') }}
      </a>
    </div>
  </div>
</template>
