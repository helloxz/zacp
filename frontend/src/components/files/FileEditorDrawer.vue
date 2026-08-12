<script setup lang="ts">
/**
 * FileEditorDrawer — 文件文本编辑器抽屉（右侧 n-drawer）。
 *
 * 由文件树（FileExplorer）双击 / 右键【编辑】打开；内部承载 CodeMirror 6 编辑器：
 * - 打开时经后端 GET files/content 读取，校验链（目录/2MB/二进制/UTF-8）失败时展示错误态
 * - 语言按扩展名映射（@codemirror/lang-* 官方包 + legacy-modes 兜底）
 * - 深浅色跟随全局主题（暗色用 one-dark）
 * - 保存走 PUT files/content，携带打开时 mtime 做乐观锁；409 冲突时引导重新加载
 * - 有未保存修改时关闭需确认（保存并关闭 / 放弃修改 / 取消）
 *
 * 所有文件读写都发生在服务端 workspace 边界内，本组件不做任何路径拼接。
 */
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useDialog, useMessage } from 'naive-ui'
import {
  CloseOutline,
  DocumentTextOutline,
  WarningOutline,
} from '@vicons/ionicons5'

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
import { ApiError, fetchFileContent, saveFileContent } from '@/api'
import type { FileEntry } from '@/types/models'
import { useAppStore } from '@/stores/app'

const props = defineProps<{
  show: boolean
  workspaceId: number
  entry: FileEntry | null
}>()

const emit = defineEmits<{
  'update:show': [value: boolean]
  /** 保存成功后通知宿主（用于刷新文件树，更新大小/时间展示） */
  'saved': [path: string]
}>()

const message = useMessage()
const dialog = useDialog()
const appStore = useAppStore()

// ---------------------------------------------------------------------------
// 编辑器实例管理
// ---------------------------------------------------------------------------

const editorHost = ref<HTMLDivElement | null>(null)
let view: EditorView | null = null

/** 当前打开文件的内容 / mtime / 大小（mtime 用于保存乐观锁） */
const content = ref('')
const mtimeUnixMs = ref(0)
const size = ref(0)
const dirty = ref(false)
const loading = ref(false)
const saving = ref(false)
/** 读取失败原因（非空时展示错误态，不渲染编辑器） */
const loadError = ref('')
const cursorPos = ref({ line: 1, col: 1 })
/** 打开文件时所属的 workspace 快照：用户取消工作区切换后，旧文件仍展示在抽屉里，
 *  但保存必须拒绝（防止误写到新工作区的同名路径） */
let loadedWsId = 0

const drawerShow = computed({
  get: () => props.show,
  set: (v: boolean) => emit('update:show', v),
})

function destroyEditor() {
  view?.destroy()
  view = null
}

/** 创建/重建编辑器（每次打开文件或主题切换时调用） */
async function mountEditor(doc: string) {
  await nextTick()
  // 防御：容器未就绪时抛错走错误态，绝不静默返回（否则会表现为「界面空白」）
  if (!editorHost.value) {
    throw new Error('编辑器容器尚未就绪，请重试')
  }
  destroyEditor()

  // 暗色：one-dark 主题 + 其高亮样式兜底；亮色用默认高亮样式
  const themeExts: Extension[] = appStore.isDark
    ? [oneDark, syntaxHighlighting(oneDarkHighlightStyle, { fallback: true })]
    : []

  const lang = detectLanguage(props.entry?.path ?? '')

  // Ctrl/Cmd+S 保存：返回 true 吃掉按键，避免触发默认行为
  const saveBinding: KeyBinding = {
    key: 'Mod-s',
    run: () => {
      void save()
      return true
    },
  }

  const state = EditorState.create({
    doc,
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
        // 内容变化 → 标记未保存，并同步 content/size（保存时用最新内容）
        if (update.docChanged) {
          const docStr = update.state.doc.toString()
          content.value = docStr
          // 字节数按 UTF-8 编码计算（与后端 size 口径一致）
          size.value = new TextEncoder().encode(docStr).length
          dirty.value = true
        }
        // 光标位置 → 状态栏 Ln/Col
        if (update.selectionSet || update.docChanged) {
          const head = update.state.selection.main.head
          const line = update.state.doc.lineAt(head)
          cursorPos.value = { line: line.number, col: head - line.from + 1 }
        }
      }),
    ],
  })

  view = new EditorView({ state, parent: editorHost.value })
  view.focus()
}

// ---------------------------------------------------------------------------
// 读取 / 保存
// ---------------------------------------------------------------------------

/** 打开（或重新加载）当前 entry：失败时进入错误态（大文件/二进制/编码不支持等） */
async function load() {
  if (!props.show || !props.entry || !props.workspaceId) return
  loading.value = true
  loadError.value = ''
  dirty.value = false
  loadedWsId = props.workspaceId
  try {
    const data = await fetchFileContent(props.workspaceId, props.entry.path)
    content.value = data.content
    size.value = data.size
    mtimeUnixMs.value = data.mtimeUnixMs
    // 关键时序：必须先把 loading 置 false，模板才会从「读取中」分支切到
    // v-else 的 editorHost 分支；否则 mountEditor 拿到的 editorHost 是 null，
    // 编辑器静默不创建（表现为：后端已返回内容，界面却空白）。
    loading.value = false
    await mountEditor(data.content)
  } catch (err) {
    // 编辑器实例已随 error 态销毁宿主 DOM，必须同步销毁 view，避免悬空引用
    destroyEditor()
    loadError.value = err instanceof Error ? err.message : '文件读取失败'
  } finally {
    loading.value = false
  }
}

/**
 * 保存当前内容。返回是否保存成功。
 *
 * 乐观锁：携带打开时的 mtimeUnixMs，后端比对不一致返回 409（file_modified），
 * 说明文件已被其他端修改 —— 弹对话框引导重新加载（放弃本地改动），防止互相覆盖。
 */
async function save(): Promise<boolean> {
  if (!props.workspaceId || !props.entry || !dirty.value || saving.value) return false
  // 打开时的工作区已切换（用户取消了切换确认）：拒绝保存，防止误写到新工作区的同名文件
  if (loadedWsId !== props.workspaceId) {
    message.warning('工作区已切换，请重新打开该文件后再保存')
    return false
  }
  saving.value = true
  try {
    const data = await saveFileContent(
      props.workspaceId,
      props.entry.path,
      content.value,
      mtimeUnixMs.value,
    )
    mtimeUnixMs.value = data.mtimeUnixMs
    dirty.value = false
    message.success('已保存')
    emit('saved', data.path)
    return true
  } catch (err) {
    if (err instanceof ApiError && err.code === 'file_modified') {
      dialog.warning({
        title: '文件已被其他端修改',
        content: '保存时发现该文件已在别处被改动，直接覆盖会丢失他人修改。是否重新加载最新内容？',
        positiveText: '重新加载',
        negativeText: '取消',
        onPositiveClick: () => {
          void load()
        },
      })
    } else {
      message.error(err instanceof Error ? err.message : '保存失败')
    }
    return false
  } finally {
    saving.value = false
  }
}

/**
 * 关闭前守卫（供右上角 X / 宿主切换文件时复用）：
 * 有未保存修改时弹确认框，让用户选择「保存并关闭 / 放弃修改 / 取消」。
 * 返回 true 表示可以继续关闭/切换。
 */
function confirmClose(): Promise<boolean> {
  if (!dirty.value) return Promise.resolve(true)
  return new Promise<boolean>((resolve) => {
    dialog.warning({
      title: '未保存的修改',
      content: '当前文件有未保存的修改，关闭将丢失。',
      positiveText: '保存并关闭',
      negativeText: '放弃修改',
      closable: false,
      maskClosable: false,
      onPositiveClick: async () => {
        // 保存成功才允许继续；保存失败（如 409 冲突）保持当前状态
        const ok = await save()
        resolve(ok)
      },
      onNegativeClick: () => {
        resolve(true)
        return true
      },
      onClose: () => {
        // 点右上角 X：取消
        resolve(false)
      },
    })
  })
}

/**
 * 放弃修改守卫（供宿主切换工作区时复用）：
 * 切换工作区后文件无法再保存，因此只提供「放弃修改 / 取消」，不提供保存选项。
 */
function confirmDiscard(): Promise<boolean> {
  if (!dirty.value) return Promise.resolve(true)
  return new Promise<boolean>((resolve) => {
    dialog.warning({
      title: '未保存的修改',
      content: '切换工作区会丢失当前文件的未保存修改。',
      positiveText: '放弃修改',
      negativeText: '取消',
      closable: false,
      maskClosable: false,
      onPositiveClick: () => {
        resolve(true)
      },
      onNegativeClick: () => {
        resolve(false)
        return true
      },
      onClose: () => {
        resolve(false)
      },
    })
  })
}

/** 右上角关闭按钮：与遮罩/ESC 一样走未保存确认，确认后才真正置 show=false */
async function requestClose() {
  if (await confirmClose()) {
    drawerShow.value = false
  }
}

// 暴露给宿主（FileExplorer）：
// - dirty：切换文件 / 工作区前判断是否有未保存修改
// - confirmClose / confirmDiscard：复用本组件的确认弹窗逻辑
defineExpose({
  dirty,
  confirmClose,
  confirmDiscard,
})

// ---------------------------------------------------------------------------
// 生命周期
// ---------------------------------------------------------------------------

// 打开抽屉或切换文件时加载
watch([() => props.show, () => props.entry], () => {
  if (props.show && props.entry) {
    void load()
  }
})

// 主题切换后重建编辑器（重建会丢撤销历史，编辑中切主题属于低频操作，可接受）
watch(
  () => appStore.isDark,
  () => {
    if (view && !loading.value && !loadError.value) {
      mountEditor(content.value).catch((err) => {
        loadError.value = err instanceof Error ? err.message : '编辑器重建失败'
      })
    }
  },
)

onBeforeUnmount(() => {
  destroyEditor()
})

// ---------------------------------------------------------------------------
// 状态栏格式化工具
// ---------------------------------------------------------------------------

function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
  return `${(n / 1024 / 1024).toFixed(2)} MB`
}

function formatTime(ms: number): string {
  const d = new Date(ms)
  return `${d.toLocaleDateString()} ${d.toLocaleTimeString()}`
}
</script>

<template>
  <n-drawer
    v-model:show="drawerShow"
    placement="right"
    :width="'min(900px, 100vw)'"
    :auto-focus="false"
    :close-on-esc="!dirty"
    :mask-closable="!dirty"
  >
    <n-drawer-content :native-scrollbar="true" class="!p-0">
      <template #header>
        <div class="flex w-full min-w-0 items-center gap-2">
          <n-icon class="shrink-0 text-ink-secondary"><DocumentTextOutline /></n-icon>
          <div class="min-w-0 flex-1">
            <div class="truncate text-sm font-medium leading-5">
              {{ entry?.name || '文件' }}
            </div>
            <div class="truncate text-xs leading-4 text-ink-muted">
              {{ entry?.path }}
            </div>
          </div>
          <!-- 未保存圆点：编辑中提示，保存后消失 -->
          <span
            v-if="dirty"
            class="shrink-0 rounded bg-amber-500/10 px-1.5 py-0.5 text-xs text-amber-500"
          >
            ● 未保存
          </span>
          <n-button
            type="primary"
            size="small"
            class="shrink-0"
            :loading="saving"
            :disabled="!dirty || !entry || !!loadError"
            @click="save"
          >
            保存
          </n-button>
          <n-button
            quaternary
            circle
            size="small"
            class="shrink-0"
            title="关闭"
            @click="requestClose"
          >
            <template #icon>
              <n-icon><CloseOutline /></n-icon>
            </template>
          </n-button>
        </div>
      </template>

      <div class="flex h-full min-h-0 flex-col">
        <!-- 读取中 -->
        <div v-if="loading" class="flex flex-1 items-center justify-center gap-2">
          <n-spin size="small" />
          <span class="text-xs text-ink-muted">读取中…</span>
        </div>

        <!-- 读取失败：展示后端拒绝原因（大文件 / 二进制 / 编码 / 路径不存在等） -->
        <div
          v-else-if="loadError"
          class="flex flex-1 flex-col items-center justify-center gap-3 px-6 text-center"
        >
          <n-icon size="40" color="#e88080"><WarningOutline /></n-icon>
          <div class="max-w-sm break-all text-sm text-ink-secondary">{{ loadError }}</div>
          <n-button size="small" @click="requestClose">关闭</n-button>
        </div>

        <!-- 编辑器主体 -->
        <div v-else ref="editorHost" class="min-h-0 flex-1 overflow-hidden" />

        <!-- 状态栏：光标位置 + 编码 + 大小 + 最近保存时间 -->
        <div
          v-if="!loadError"
          class="flex items-center justify-between border-t border-divider px-3 py-1 text-[11px] text-ink-muted"
        >
          <span>Ln {{ cursorPos.line }}, Col {{ cursorPos.col }}</span>
          <span class="flex items-center gap-3">
            <span>UTF-8</span>
            <span>{{ formatBytes(size) }}</span>
            <span v-if="!dirty && mtimeUnixMs" class="hidden sm:inline">
              {{ formatTime(mtimeUnixMs) }}
            </span>
          </span>
        </div>
      </div>
    </n-drawer-content>
  </n-drawer>
</template>

<style scoped>
/* CodeMirror 容器高度自适应；CM6 的基础样式由各包以 StyleModule 注入，无需手动引 CSS */
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

/* 抽屉高度链（native-scrollbar 模式，naive 自带）：
   drawer(100%) → content-wrapper(100%) → n-drawer-content(100%, flex column)
   → n-drawer-body(flex:1) → n-drawer-body-content-wrapper(height:100%)
   → 内容容器(h-full, flex column) → editorHost(flex-1, min-h-0) + 状态栏(固定高度)。
   这样编辑器永远占满剩余可用高度；滚动只发生在 CM6 内部（.cm-scroller），
   状态栏作为固定高度 flex item 永远钉在底部。
   注意：naive NScrollbar 模式（native-scrollbar=false）时 NScrollbar 根高度由内容决定，
   无法撑满，因此必须使用 native-scrollbar 模式。 */
:deep(.n-drawer-body) {
  flex: 1 0 0;
  overflow: hidden;
}
:deep(.n-drawer-body-content-wrapper) {
  height: 100%;
  overflow: hidden; /* 关闭外层滚动，滚动全部交给编辑器自身 */
  padding: 0;
}
</style>
