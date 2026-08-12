<script setup lang="ts">
/**
 * AgentConfigEditorModal — 设置页「智能体 - 编辑配置」弹窗。
 *
 * 打开时向后端拉取该智能体「真实存在」的配置文件列表（后端已按 HOME 展开过滤），
 * 用折叠面板展示多个配置文件（默认展开第一个，其余折叠）；每个文件一个 CodeMirror 6 编辑器。
 *
 * - 语言按扩展名经共享的 utils/codeMirrorLang 推断（toml/yaml/json/.env 均已支持）
 * - 保存走 PUT config-files/content：后端做格式语法校验（json/yaml/toml）+ mtime 乐观锁；
 *   409（file_modified）时引导重新加载，400（invalid_syntax）直接提示后端错误信息
 * - 深浅色跟随全局主题（暗色用 one-dark）
 * - 有未保存修改时关闭需确认（放弃修改 / 取消）
 *
 * 编辑器实例按「当前展开的面板」挂载：折叠即销毁（内容已同步到状态，重新展开时重建），
 * 避免在 display:none 容器里创建 CodeMirror 导致的测量问题。
 */
import { nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useDialog, useMessage } from 'naive-ui'
import { CloseOutline, DocumentTextOutline } from '@vicons/ionicons5'
import { useI18n } from 'vue-i18n'

import { EditorState, type Extension } from '@codemirror/state'
import {
  EditorView,
  crosshairCursor,
  drawSelection,
  dropCursor,
  highlightActiveLine,
  highlightActiveLineGutter,
  keymap,
  lineNumbers,
  rectangularSelection,
  type KeyBinding,
} from '@codemirror/view'
import { defaultKeymap, history, historyKeymap, indentWithTab } from '@codemirror/commands'
import {
  bracketMatching,
  defaultHighlightStyle,
  foldGutter,
  indentOnInput,
  syntaxHighlighting,
} from '@codemirror/language'
import { autocompletion, closeBrackets, closeBracketsKeymap } from '@codemirror/autocomplete'
import { highlightSelectionMatches, searchKeymap } from '@codemirror/search'
import { oneDark, oneDarkHighlightStyle } from '@codemirror/theme-one-dark'

import { detectLanguage } from '@/utils/codeMirrorLang'
import { ApiError, fetchAgentConfigContent, fetchAgentConfigFiles, saveAgentConfigContent } from '@/api'
import type { AgentConfigFile, ManageAgent } from '@/types/models'
import { useAppStore } from '@/stores/app'

const props = defineProps<{
  show: boolean
  agent: ManageAgent | null
}>()

const emit = defineEmits<{
  'update:show': [value: boolean]
}>()

const { t } = useI18n()
const message = useMessage()
const dialog = useDialog()
const appStore = useAppStore()

/** 单个配置文件的编辑器状态（后端返回的基础信息 + 本地编辑态） */
interface EditorFile extends AgentConfigFile {
  content: string
  size: number
  /** 打开时记录的 mtime（毫秒），保存时回传做乐观锁 */
  mtimeUnixMs: number
  dirty: boolean
  loading: boolean
  saving: boolean
  loadError: string
}

const files = ref<EditorFile[]>([])
/** 当前展开的面板（文件 path 列表）；打开时默认展开第一个 */
const expandedNames = ref<string[]>([])
const loading = ref(false)
const listError = ref('')

/** 已挂载的编辑器实例：path → EditorView（普通 Map，无需响应式） */
const views = new Map<string, EditorView>()
/** 编辑器容器 DOM：path → host（由模板函数 ref 收集） */
const editorHosts = new Map<string, HTMLDivElement>()

/** 本次打开时所属的 agentId 快照：防止异步返回时弹窗已切到其他智能体 */
let loadedAgentId = ''

function setHost(path: string, el: unknown) {
  if (el instanceof HTMLDivElement) {
    editorHosts.set(path, el)
  } else {
    editorHosts.delete(path)
  }
}

/** 销毁全部编辑器并清空状态（每次打开前调用） */
function resetState() {
  for (const view of views.values()) view.destroy()
  views.clear()
  editorHosts.clear()
  files.value = []
  expandedNames.value = []
  loading.value = false
  listError.value = ''
}

/**
 * 打开弹窗：拉取存在的配置文件列表 → 默认展开第一个 → 并行读取各文件内容 → 挂载编辑器。
 * 单个文件读取失败不影响其余文件（该面板显示错误态）。
 */
async function open() {
  if (!props.agent) return
  loadedAgentId = props.agent.agentId
  resetState()
  loading.value = true
  listError.value = ''
  try {
    const list = await fetchAgentConfigFiles(loadedAgentId)
    // 竞态守卫：列表返回期间弹窗可能已关闭或切到其他智能体，丢弃旧响应，
    // 避免 A 的异步结果覆盖 B 的 files/展开状态。
    if (!props.agent || loadedAgentId !== props.agent.agentId) {
      return
    }
    if (!list.length) {
      // 无可用文件：展示空态（按钮可见但文件都不存在的情况）
      return
    }
    files.value = list.map((f) => ({
      ...f,
      content: '',
      size: 0,
      mtimeUnixMs: 0,
      dirty: false,
      loading: true,
      saving: false,
      loadError: '',
    }))
    expandedNames.value = [list[0].path]

    await Promise.all(
      files.value.map(async (f) => {
        try {
          const data = await fetchAgentConfigContent(loadedAgentId, f.path)
          f.content = data.content
          f.size = data.size
          f.mtimeUnixMs = data.mtimeUnixMs
        } catch (e) {
          f.loadError = e instanceof Error ? e.message : t('settings.agent.configLoadFailed')
        } finally {
          f.loading = false
        }
      }),
    )
  } catch (e) {
    listError.value = e instanceof Error ? e.message : t('settings.agent.configLoadFailed')
  } finally {
    loading.value = false
  }
  // 关键时序：必须在 loading 置 false 之后再挂载编辑器——loading=true 期间模板渲染的是
  // n-spin，<n-collapse> 分支（含编辑器容器）尚未渲染，editorHosts 为空，
  // mountExpanded 会静默跳过（不报错也不重试），导致首个面板内容空白、
  // 直到再次展开/折叠触发 watch 才补挂载。
  await nextTick()
  mountExpanded()
}

// ---------------------------------------------------------------------------
// CodeMirror 实例管理
// ---------------------------------------------------------------------------

/** 为文件创建一个编辑器并挂载到 host（extensions 与工作区编辑器 FileEditorDrawer 保持一致） */
function createEditor(host: HTMLDivElement, file: EditorFile): EditorView {
  // 暗色：one-dark 主题 + 其高亮样式兜底；亮色用默认高亮样式
  const themeExts: Extension[] = appStore.isDark
    ? [oneDark, syntaxHighlighting(oneDarkHighlightStyle, { fallback: true })]
    : []
  const lang = detectLanguage(file.path)

  // Ctrl/Cmd+S 保存当前文件：返回 true 吃掉按键，避免触发默认行为
  const saveBinding: KeyBinding = {
    key: 'Mod-s',
    run: () => {
      void save(file)
      return true
    },
  }

  const state = EditorState.create({
    doc: file.content,
    extensions: [
      lineNumbers(),
      highlightActiveLineGutter(),
      history(),
      foldGutter(),
      drawSelection(),
      dropCursor(),
      EditorState.allowMultipleSelections.of(true),
      indentOnInput(),
      syntaxHighlighting(defaultHighlightStyle, { fallback: true }),
      bracketMatching(),
      closeBrackets(),
      autocompletion(),
      rectangularSelection(),
      crosshairCursor(),
      highlightActiveLine(),
      highlightSelectionMatches(),
      keymap.of([
        ...closeBracketsKeymap,
        ...defaultKeymap,
        ...searchKeymap,
        ...historyKeymap,
        indentWithTab,
        saveBinding,
      ]),
      ...themeExts,
      ...(lang ? [lang] : []),
      EditorView.updateListener.of((update) => {
        // 内容变化 → 同步到状态并标记未保存（保存时用最新内容）
        if (update.docChanged) {
          file.content = update.state.doc.toString()
          file.dirty = true
        }
      }),
    ],
  })
  const view = new EditorView({ state, parent: host })
  view.focus()
  return view
}

/** 为所有已展开且已加载完成的文件挂载编辑器（幂等：已有实例的跳过） */
function mountExpanded() {
  for (const path of expandedNames.value) {
    const file = files.value.find((f) => f.path === path)
    if (!file || file.loading || file.loadError || views.has(path)) continue
    const host = editorHosts.get(path)
    if (!host) continue
    try {
      views.set(path, createEditor(host, file))
    } catch (e) {
      file.loadError = e instanceof Error ? e.message : '编辑器初始化失败'
    }
  }
}

/** 折叠的面板销毁编辑器；新展开的挂载。内容始终在 file.content，重建不丢数据。 */
watch(expandedNames, () => {
  for (const [path, view] of views) {
    if (!expandedNames.value.includes(path)) {
      view.destroy()
      views.delete(path)
    }
  }
  void nextTick().then(mountExpanded)
})

// ---------------------------------------------------------------------------
// 读取 / 保存
// ---------------------------------------------------------------------------

/**
 * 保存单个文件。返回是否保存成功。
 * 乐观锁：携带打开时的 mtimeUnixMs，后端比对不一致返回 409（file_modified）→
 * 弹对话框引导重新加载；语法错误返回 400（invalid_syntax）→ 直接提示后端错误详情。
 */
async function save(file: EditorFile): Promise<boolean> {
  if (!props.agent || file.saving || !file.dirty) return false
  file.saving = true
  try {
    const data = await saveAgentConfigContent(
      props.agent.agentId,
      file.path,
      file.content,
      file.mtimeUnixMs,
    )
    file.mtimeUnixMs = data.mtimeUnixMs
    file.size = data.size
    file.dirty = false
    message.success(t('settings.agent.configSaved'))
    return true
  } catch (err) {
    if (err instanceof ApiError && err.code === 'file_modified') {
      const reloadNow = await new Promise<boolean>((resolve) => {
        dialog.warning({
          title: t('settings.agent.fileModifiedTitle'),
          content: t('settings.agent.fileModifiedContent'),
          positiveText: t('settings.agent.reload'),
          negativeText: t('common.cancel'),
          closable: false,
          maskClosable: false,
          onPositiveClick: () => resolve(true),
          onNegativeClick: () => {
            resolve(false)
            return true
          },
          onClose: () => resolve(false),
        })
      })
      if (reloadNow) await reload(file)
    } else {
      message.error(err instanceof Error ? err.message : t('settings.agent.configSaveFailed'))
    }
    return false
  } finally {
    file.saving = false
  }
}

/** 重新拉取文件内容并重建编辑器（409 冲突后由用户确认触发） */
async function reload(file: EditorFile) {
  if (!props.agent) return
  file.loading = true
  file.loadError = ''
  try {
    const data = await fetchAgentConfigContent(props.agent.agentId, file.path)
    file.content = data.content
    file.size = data.size
    file.mtimeUnixMs = data.mtimeUnixMs
    file.dirty = false
  } catch (e) {
    file.loadError = e instanceof Error ? e.message : t('settings.agent.configLoadFailed')
    return
  } finally {
    file.loading = false
  }
  // 注意：必须在 loading 置 false 之后再销毁重建——mountExpanded 会跳过 loading 中的文件，
  // 若在 finally 之前调用会导致编辑器不重新挂载（409 重载后容器空白）。
  const old = views.get(file.path)
  if (old) {
    old.destroy()
    views.delete(file.path)
  }
  await nextTick()
  mountExpanded()
}

/** 关闭守卫：有未保存修改时弹确认，确认后才真正关闭 */
function requestClose() {
  if (files.value.some((f) => f.dirty)) {
    dialog.warning({
      title: t('settings.agent.unsavedTitle'),
      content: t('settings.agent.unsavedContent'),
      positiveText: t('settings.agent.discard'),
      negativeText: t('common.cancel'),
      closable: false,
      maskClosable: false,
      onPositiveClick: () => {
        emit('update:show', false)
      },
      onNegativeClick: () => true,
    })
    return
  }
  emit('update:show', false)
}

/** n-modal 的 update:show（点遮罩/右上角 X 关闭时仍走未保存确认） */
function onShowChange(v: boolean) {
  if (!v) requestClose()
}

// ---------------------------------------------------------------------------
// 生命周期
// ---------------------------------------------------------------------------

watch(
  () => props.show,
  (v) => {
    if (v && props.agent) void open()
  },
)

// 主题切换后重建已挂载的编辑器（重建会丢撤销历史，编辑中切主题属低频操作，可接受）
watch(
  () => appStore.isDark,
  () => {
    if (!props.show || views.size === 0) return
    for (const path of [...views.keys()]) {
      views.get(path)?.destroy()
      views.delete(path)
    }
    void nextTick().then(mountExpanded)
  },
)

onBeforeUnmount(() => {
  for (const view of views.values()) view.destroy()
  views.clear()
})
</script>

<template>
  <n-modal :show="show" :mask-closable="true" @update:show="onShowChange">
    <!-- 自绘弹窗容器：圆角/背景/阴影与设置弹窗（SettingsModal）保持一致。
         高度自动伸展，但上限为视口高度减上下留白（calc(100vh - 2rem)），
         超高时滚动发生在下方内容区，标题栏保持固定 -->
    <div
      class="flex max-h-[calc(100vh-2rem)] w-[960px] max-w-[94vw] flex-col overflow-hidden rounded-2xl bg-surface-raised shadow-2xl"
    >
      <!-- 顶部标题栏：与设置弹窗同一风格（border-b 分隔线 + 圆形关闭按钮） -->
      <header
        class="flex shrink-0 items-center justify-between border-b border-divider-subtle px-6 py-4"
      >
        <h2 class="text-base font-semibold text-ink">
          {{ t('settings.agent.configFilesTitle', { name: agent?.name ?? '' }) }}
        </h2>
        <button
          type="button"
          :aria-label="t('settings.close')"
          class="flex h-8 w-8 cursor-pointer items-center justify-center rounded-full text-ink-muted transition-colors hover:bg-surface-hover hover:text-ink-secondary focus:outline-none focus-visible:ring-2 focus-visible:ring-divider"
          @click="onShowChange(false)"
        >
          <n-icon :size="18"><CloseOutline /></n-icon>
        </button>
      </header>

      <!-- 内容区：flex-1 + min-h-0 让滚动落在这里（标题栏 shrink-0 固定在顶部）；
           内容未超高时高度随内容自动伸展 -->
      <div class="flex min-h-0 flex-1 flex-col gap-3 overflow-y-auto p-6">
        <!-- 打开中 -->
        <n-spin v-if="loading" class="py-12" />

        <!-- 列表加载失败 -->
        <div
          v-else-if="listError"
          class="flex flex-col items-center gap-3 rounded-xl border border-dashed border-rose-200 bg-rose-50/60 px-6 py-10 text-center dark:border-rose-900/50 dark:bg-rose-950/30"
        >
          <p class="text-sm text-rose-500 dark:text-rose-400">{{ listError }}</p>
          <n-button size="small" type="primary" tertiary @click="open">
            {{ t('dirPicker.retry') }}
          </n-button>
        </div>

        <!-- 空态：按钮可见但所有候选文件都不存在 -->
        <div
          v-else-if="!files.length"
          class="rounded-xl border border-dashed border-divider py-10 text-center text-sm text-ink-muted"
        >
          {{ t('settings.agent.configFilesEmpty') }}
        </div>

        <!-- 折叠面板：默认展开第一个，其余折叠；可同时展开多个对比；高度随内容自适应 -->
        <n-collapse
          v-else
          v-model:expanded-names="expandedNames"
          class="agent-config-collapse"
        >
          <n-collapse-item v-for="file in files" :key="file.path" :name="file.path">
            <template #header>
              <div class="flex min-w-0 flex-1 items-center gap-2">
                <n-icon :size="16" class="shrink-0 text-ink-muted"><DocumentTextOutline /></n-icon>
                <span class="truncate font-medium text-ink">{{ file.name }}</span>
                <n-tag v-if="file.ext" size="tiny" :bordered="false" type="info">
                  {{ file.ext }}
                </n-tag>
                <span v-if="file.dirty" class="shrink-0 text-xs text-amber-500">
                  {{ t('settings.agent.unsavedTag') }}
                </span>
              </div>
            </template>

            <div class="flex flex-col gap-2">
              <!-- 单文件读取失败（不影响其他文件编辑） -->
              <div
                v-if="file.loadError"
                class="flex items-center justify-between gap-3 rounded-lg border border-dashed border-rose-200 bg-rose-50/60 px-4 py-3 text-sm text-rose-500 dark:border-rose-900/50 dark:bg-rose-950/30"
              >
                <span class="truncate">{{ file.loadError }}</span>
                <n-button size="tiny" tertiary type="primary" @click="reload(file)">
                  {{ t('dirPicker.retry') }}
                </n-button>
              </div>

              <!-- 编辑器容器：仅展开时渲染（v-show + 固定高度，避免折叠动画影响 CM 测量） -->
              <div
                v-show="expandedNames.includes(file.path) && !file.loadError"
                :ref="(el: unknown) => setHost(file.path, el)"
                class="h-72 overflow-hidden rounded-lg border border-divider bg-white dark:bg-[#282c34]"
              />

              <!-- 底部工具栏：文件路径 + 保存 -->
              <div class="flex items-center justify-between gap-3">
                <span class="truncate text-xs text-ink-muted">{{ file.path }}</span>
                <n-button
                  size="small"
                  :type="file.dirty ? 'primary' : 'default'"
                  :disabled="!file.dirty || file.saving || !!file.loadError || file.loading"
                  :loading="file.saving"
                  @click="save(file)"
                >
                  {{ t('common.save') }}
                </n-button>
              </div>
            </div>
          </n-collapse-item>
        </n-collapse>
      </div>
    </div>
  </n-modal>
</template>

<style scoped>
/* CodeMirror 容器高度固定（h-72），CM 100% 填充；基础样式由各包以 StyleModule 注入 */
:deep(.cm-editor) {
  height: 100%;
  font-size: 13px;
}
:deep(.cm-editor.cm-focused) {
  outline: none;
}
:deep(.cm-scroller) {
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, 'Liberation Mono', monospace;
}
</style>
