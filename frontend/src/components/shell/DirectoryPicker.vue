<script setup lang="ts">
/**
 * DirectoryPicker — 新建项目弹窗的目录选择器。
 *
 * 三件套：路径输入框（可手动编辑）+ 面包屑/返回上级 + 子文件夹列表。
 * - 打开弹窗（本组件重新挂载）时自动请求初始目录：后端返回
 *   session.default_cwd 解析后的绝对路径；
 * - 单击文件夹 = 进入并加载其子目录，路径输入框自动同步为当前目录；
 * - 输入框保留手动输入能力，回车通过 submit 事件交给父组件提交。
 * 与后端约定：GET /api/v1/fs/directories?path=<绝对路径>（见 api/index.ts）。
 */
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { ArrowBackOutline, FolderOpenOutline } from '@vicons/ionicons5'
import { NButton, NIcon, NInput, NSpin } from 'naive-ui'
import { fetchDirectories } from '@/api'
import type { DirectoryEntry } from '@/types/models'

/** 双向绑定：进入目录后把当前绝对路径写回父组件（弹窗路径输入框） */
const modelValue = defineModel<string>({ default: '' })

const emit = defineEmits<{
  /** 输入框内回车：由父组件触发「创建项目」提交 */
  (e: 'submit'): void
}>()

const { t } = useI18n()

/** 当前浏览目录（绝对路径） */
const currentPath = ref('')
/** 上级目录绝对路径；'' = 已到根目录（禁用「返回上级」） */
const parentPath = ref('')
/** 子文件夹列表 */
const entries = ref<DirectoryEntry[]>([])
const loading = ref(false)
/** 最近一次加载错误（后端错误信息，如无权限/路径不存在）；进入目录成功即清空 */
const error = ref('')
/** 最近一次请求的目录（'' = 后端默认目录），供重试使用 */
let lastRequestedPath = ''

/** 面包屑：按 / 分段（保留前缀），最后一段为当前目录；根目录显示 "/" */
const crumbs = computed(() => {
  const items: { label: string; path: string }[] = []
  for (const seg of currentPath.value.split('/').filter(Boolean)) {
    items.push({ label: seg, path: items.length ? items[items.length - 1].path + '/' + seg : '/' + seg })
  }
  if (items.length === 0) items.push({ label: '/', path: '/' })
  return items
})

/**
 * 加载指定目录；path 省略时由后端返回默认工作目录（session.default_cwd）。
 * 失败时保留旧列表仅展示错误（用户可重试或手动改路径），不破坏当前浏览状态。
 * loading 中忽略新请求（防面包屑/列表连点）。
 */
async function load(path?: string) {
  if (loading.value) return
  lastRequestedPath = path ?? ''
  loading.value = true
  error.value = ''
  try {
    const data = await fetchDirectories(path)
    currentPath.value = data.path
    parentPath.value = data.parent
    entries.value = data.entries
    // 导航（path 非空）必同步输入框；初始加载时若用户已手动输入过
    // 路径则不覆盖（仅当输入框仍为空才写回默认工作目录）
    if (path || !modelValue.value) {
      modelValue.value = data.path
    }
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  } finally {
    loading.value = false
  }
}

/** 单击文件夹 → 进入并加载子目录（loading 中忽略重复点击） */
function enterDir(entry: DirectoryEntry) {
  if (loading.value) return
  void load(entry.path)
}

/** 返回上级目录 */
function goUp() {
  if (!parentPath.value || loading.value) return
  void load(parentPath.value)
}

// n-modal 默认 display-directive="if"：弹窗关闭时内容销毁、打开时重新挂载，
// 因此这里直接加载初始目录即可，无需额外暴露 reset 给父组件。
onMounted(() => {
  void load()
})
</script>

<template>
  <div class="space-y-2">
    <!-- 路径输入框：可手动编辑；回车交由父组件提交 -->
    <n-input
      v-model:value="modelValue"
      size="small"
      :placeholder="t('shell.newProjectPlaceholder')"
      @keydown.enter="emit('submit')"
    />

    <!-- 面包屑 + 返回上级 -->
    <div class="flex items-center gap-1">
      <n-button
        quaternary
        circle
        size="tiny"
        :disabled="!parentPath || loading"
        :title="t('dirPicker.up')"
        @click="goUp"
      >
        <template #icon>
          <n-icon><ArrowBackOutline /></n-icon>
        </template>
      </n-button>
      <div
        class="flex min-w-0 flex-1 items-center gap-0.5 overflow-x-auto whitespace-nowrap text-xs text-slate-500"
      >
        <template v-for="(c, i) in crumbs" :key="c.path">
          <button
            class="shrink-0 rounded px-1 py-0.5 hover:bg-slate-100 hover:text-slate-800"
            :class="{ 'font-medium text-slate-800': i === crumbs.length - 1 }"
            @click="load(c.path)"
          >
            {{ c.label }}
          </button>
          <span v-if="i < crumbs.length - 1" class="text-slate-300">/</span>
        </template>
      </div>
    </div>

    <!-- 子文件夹列表 -->
    <n-spin :show="loading">
      <div class="max-h-56 overflow-y-auto rounded border border-slate-200 bg-white">
        <!-- 加载失败：展示错误 + 重试（重试回到失败的那个目录，而非默认目录） -->
        <div
          v-if="error"
          class="flex items-center justify-between gap-2 px-3 py-2 text-xs text-red-500"
        >
          <span class="min-w-0 truncate">{{ error }}</span>
          <n-button size="tiny" quaternary type="error" @click="load(lastRequestedPath)">
            {{ t('dirPicker.retry') }}
          </n-button>
        </div>
        <!-- 空目录 -->
        <div v-else-if="entries.length === 0" class="px-3 py-6 text-center text-xs text-slate-400">
          {{ t('dirPicker.empty') }}
        </div>
        <!-- 文件夹列表：单击进入 -->
        <template v-else>
          <button
            v-for="entry in entries"
            :key="entry.path"
            class="flex w-full items-center gap-2 px-3 py-1.5 text-left text-sm hover:bg-slate-50"
            @click="enterDir(entry)"
          >
            <n-icon class="shrink-0 text-amber-500"><FolderOpenOutline /></n-icon>
            <span class="truncate">{{ entry.name }}</span>
          </button>
        </template>
      </div>
    </n-spin>
  </div>
</template>
