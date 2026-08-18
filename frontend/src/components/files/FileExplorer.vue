<script setup lang="ts">
/**
 * FileExplorer — 「文件」Tab 单目录浏览。
 *
 * 功能：
 * - 扁平目录浏览：仅显示当前目录下的文件与文件夹；单击文件夹进入，
 *   列表顶部 `...` 返回上级。不做多级树形展开，避免大项目同时展开多个
 *   子目录导致 DOM 节点暴增卡顿（性能收益：数据与渲染量恒为「当前目录」）。
 * - 路径输入栏：前缀为项目根目录名（锁定不可改，从根上杜绝路径穿透），
 *   其后为可编辑的相对路径；回车或点【进入】跳转，点【刷新】重载当前目录。
 *   输入校验拒绝绝对路径与 `..`（后端 resolveInWorkspace 另有硬校验兜底）。
 * - 拖拽上传：拖入列表 = 上传到当前目录（根目录时上传到项目根）。
 * - 粘贴上传：焦点在列表区域时 Ctrl/Cmd+V 粘贴文件/截图，同样上传到当前目录。
 *   仅 Chrome/Edge/Firefox 支持（Safari 剪贴板不向网页暴露文件）；
 *   粘贴/拖拽单次统一上限 10 个文件，超过拒绝整批；截图自动命名并转 webp。
 * - 图片自动压缩转 webp（等比不裁剪，>5MB 降采样兜底），非图片原样直传。
 * - 单击图片文件 → n-image 原生预览；双击文本文件 → 编辑器；右键 →
 *   「重命名 / 复制名称」（文件另加「编辑」，非文本文件不显示）。
 *   可编辑判定见 utils/fileEditable.ts（扩展名白名单，与后端 Read/Write 限制一致）。
 *
 * 数据根 = 当前会话所属 workspace；无会话时用默认 workspace。
 */
import { computed, onMounted, ref, watch } from 'vue'
import { NIcon, useMessage, type DropdownOption } from 'naive-ui'
import {
  FolderOutline,
  DocumentOutline,
  ImageOutline,
  RefreshOutline,
  ArrowForwardOutline,
} from '@vicons/ionicons5'
import { fetchFiles, fetchPreviewUrl, renameFile, deleteFile, uploadFiles } from '@/api'
import {
  extractPastedFiles,
  isEditableTarget,
  isPasteSupported,
  prepareFile,
  MAX_UPLOAD_FILES,
} from '@/utils/fileUpload'
import type { FileEntry } from '@/types/models'
import { useSessionStore } from '@/stores/session'
import { copyText } from '@/utils/clipboard'
import { isEditableFileName } from '@/utils/fileEditable'
import FileEditorDrawer from '@/components/files/FileEditorDrawer.vue'

const message = useMessage()
const sessionStore = useSessionStore()

/** 当前 workspace：优先当前会话所属项目，其次默认项目 */
const activeWs = computed(() => {
  const sid = sessionStore.activeSession?.workspace?.id
  if (sid) {
    return sessionStore.workspaces.find((w) => w.id === sid)
  }
  return sessionStore.defaultWorkspace()
})
const workspaceId = computed(() => activeWs.value?.id ?? 0)

/** 根目录名（路径栏锁定前缀）：取 workspace 绝对路径最后一段，如 /data/apps/zurl → zurl */
const wsBaseName = computed(() => {
  const p = activeWs.value?.path ?? ''
  return p.replace(/\/+$/, '').split('/').pop() || ''
})

// ---------------------------------------------------------------------------
// 目录浏览状态：只保留当前目录，切换即整体替换，不驻留历史数据
// ---------------------------------------------------------------------------

/** 当前目录（相对 workspace 根的路径，`/` 分隔；'' = 根） */
const currentDir = ref('')
/** 路径输入框内容（可编辑的相对路径部分，不含锁定前缀） */
const pathInput = ref('')
/** 当前目录条目（后端已排序：目录在前、按名称） */
const entries = ref<FileEntry[]>([])
const listLoading = ref(false)
/** 非根目录时在列表顶部显示 `...` 返回上级 */
const canGoUp = computed(() => currentDir.value !== '')

/**
 * 解析并校验用户输入的相对路径；非法返回 null，空串表示根目录。
 * 拒绝：绝对路径（/ 开头）、Windows 盘符（C:\…）、`..` 越界段；
 * Windows 反斜杠统一转正斜杠。
 * 这是防穿透的第一道闸（前端 UX 层），后端 resolveInWorkspace 是最终硬校验。
 */
function normalizeInputPath(raw: string): string | null {
  const v = raw.trim().replace(/\\/g, '/')
  if (!v) return ''
  if (v.startsWith('/')) return null
  if (/^[A-Za-z]:/.test(v)) return null
  const segs = v.split('/').filter(Boolean)
  if (segs.some((s) => s === '..')) return null
  return segs.join('/')
}

/**
 * 加载指定目录：成功后用后端规范化后的 path 同步输入框
 * （用户输入 `a//b` 这类脏路径时，展示自动对齐为后端 Clean 结果）。
 * 竞态防护：每次请求递增序号，过期响应直接丢弃（快速连点进入/切换
 * workspace 时，先发的慢请求不会覆盖新状态）。
 */
let loadSeq = 0
async function load(dir: string) {
  if (!workspaceId.value) return
  const seq = ++loadSeq
  listLoading.value = true
  try {
    const data = await fetchFiles(workspaceId.value, dir)
    if (seq !== loadSeq) return // 已有更新的请求，丢弃本次结果
    currentDir.value = data.path
    pathInput.value = data.path
    entries.value = data.entries
  } catch (err) {
    if (seq !== loadSeq) return
    // 失败时保持「路径栏 = 当前目录 = 列表」一致：输入框回滚到当前实际目录
    pathInput.value = currentDir.value
    message.error(err instanceof Error ? err.message : '加载文件列表失败')
  } finally {
    if (seq === loadSeq) listLoading.value = false
  }
}

/** 回车 / 【进入】按钮：校验输入后跳转 */
async function enterPath() {
  const target = normalizeInputPath(pathInput.value)
  if (target === null) {
    message.warning('路径不能包含「..」，也不能使用绝对路径')
    return
  }
  if (target === currentDir.value) return
  await load(target)
}

/** 【刷新】：重载当前目录（输入框保持用户手输内容不变） */
async function reload() {
  await load(currentDir.value)
}

/** 单击文件夹 → 进入子目录 */
function enterDir(entry: FileEntry) {
  if (listLoading.value) return
  void load(entry.path)
}

/** 列表顶部 `...` → 返回上级目录 */
function goUp() {
  if (!currentDir.value || listLoading.value) return
  const i = currentDir.value.lastIndexOf('/')
  void load(i >= 0 ? currentDir.value.slice(0, i) : '')
}

// workspace 变化 → 回到根目录重载；同时关闭可能指向旧 workspace 的编辑器
watch(workspaceId, async () => {
  // 有未保存修改时先征得用户同意（切换后旧文件无法再保存，故只提供「放弃/取消」）；
  // 用户取消 → 保持抽屉打开（内容仍可复制），但列表照常切换到新 workspace
  if (editorRef.value?.dirty) {
    if (await editorRef.value.confirmDiscard()) {
      editorShow.value = false
    }
  } else {
    editorShow.value = false
  }
  currentDir.value = ''
  pathInput.value = ''
  entries.value = []
  void load('')
})

onMounted(() => {
  void load('')
})

/**
 * 外部上传完成（如 Composer 输入框粘贴上传）后刷新列表：
 * 监听 store 版本号而非组件间直接调用，避免兄弟组件耦合；
 * 只刷新当前目录，不打断用户正在浏览的位置。
 */
watch(
  () => sessionStore.fileListVersion,
  () => {
    void reload()
  },
)

// ---------------------------------------------------------------------------
// 右键菜单（重命名 / 复制名称）
// ---------------------------------------------------------------------------

const ctxMenuShow = ref(false)
const ctxMenuX = ref(0)
const ctxMenuY = ref(0)
const ctxMenuEntry = ref<FileEntry | null>(null)
/** 右键菜单：文件多一个「编辑」项（目录不可编辑；非白名单文本类型的文件同样不显示） */
const ctxOptions = computed<DropdownOption[]>(() => {
  const options: DropdownOption[] = [
    { key: 'rename', label: '重命名' },
    { key: 'copy-name', label: '复制名称' },
  ]
  // 下载：仅文件（目录不可下载）；所有文件类型均可，100M 上限在 downloadEntry 拦截
  if (ctxMenuEntry.value && !ctxMenuEntry.value.isDir) {
    options.unshift({ key: 'download', label: '下载' })
  }
  // 仅常见文本文件（白名单扩展名 / 无扩展名 / 隐藏文件）提供编辑入口，
  // mp3/mp4/zip 等二进制文件不出现「编辑」，与后端 Read/Write 限制一致
  if (
    ctxMenuEntry.value &&
    !ctxMenuEntry.value.isDir &&
    isEditableFileName(ctxMenuEntry.value.name)
  ) {
    options.unshift({ key: 'edit', label: '编辑' })
  }
  // 删除不可逆，标红且放最底部；class 通过 props 透传到 dropdown 条目 DOM
  options.push({ key: 'delete', label: '删除', props: { class: 'text-red-500!' } })
  return options
})
const renameModalVisible = ref(false)
const renameValue = ref('')
const renaming = ref(false)
const renameTarget = ref<{ workspaceId: number; entry: FileEntry } | null>(null)

/** 右键条目：定位目标条目并弹出菜单（条目从 v-for 直接传入，无需 DOM 查找） */
function onEntryContextMenu(e: MouseEvent, entry: FileEntry) {
  ctxMenuEntry.value = entry
  ctxMenuX.value = e.clientX
  ctxMenuY.value = e.clientY
  ctxMenuShow.value = true
}

async function onCtxSelect(key: string | number) {
  ctxMenuShow.value = false
  const entry = ctxMenuEntry.value
  if (!entry) return

  // 右键【编辑】：与双击行为一致，打开文本编辑器
  if (key === 'edit') {
    await openEditor(entry)
    return
  }
  // 右键【下载】：与图片预览同一链路换直链，浏览器原生落盘
  if (key === 'download') {
    await downloadEntry(entry)
    return
  }
  if (key === 'rename') {
    renameTarget.value = { workspaceId: workspaceId.value, entry }
    renameValue.value = entry.name
    renameModalVisible.value = true
    return
  }
  if (key === 'delete') {
    deleteTarget.value = { workspaceId: workspaceId.value, entry }
    deleteValue.value = ''
    deleteModalVisible.value = true
    return
  }
  if (key !== 'copy-name') return

  const ok = await copyText(entry.name)
  if (ok) {
    message.success(`已复制「${entry.name}」`)
  } else {
    message.error('复制失败，请手动复制名称')
  }
}

/** 单文件下载上限（与后端 MaxDownloadSizeBytes 对齐）：超出直接拦截，不发请求 */
const MAX_DOWNLOAD_SIZE = 100 * 1024 * 1024

/**
 * 右键【下载】：换 12h 资源 token 直链后，经浏览器原生 <a> 触发下载。
 * 追加 download=1 让后端返回 Content-Disposition: attachment 强制落盘——
 * download 属性仅对同源/blob URL 生效，跨源部署下会被浏览器忽略
 * （跨端口即跨源，如 dev 前端 :8681 直连后端 :8680），必须靠响应头；
 * 见后端 RawFile 的 download 分支。
 * 文件名由响应头 basename 决定；不经 JS blob，100M 以内不占前端内存；
 * 后端 raw 端点仍强校验（≤100M），超限 413 的错误信息透出。
 */
async function downloadEntry(entry: FileEntry) {
  if (entry.size != null && entry.size > MAX_DOWNLOAD_SIZE) {
    message.warning(`「${entry.name}」超过 100M，不支持下载`)
    return
  }
  if (!workspaceId.value) return
  try {
    const url = await fetchPreviewUrl(workspaceId.value, entry.path)
    const a = document.createElement('a')
    // 直链已含 path 与 token query，追加 download=1（顺序无关）
    a.href = `${url}${url.includes('?') ? '&' : '?'}download=1`
    document.body.appendChild(a)
    a.click()
    a.remove()
  } catch (err) {
    message.error(err instanceof Error ? err.message : '下载失败')
  }
}

// ---------------------------------------------------------------------------
// 文本编辑器（双击文件 / 右键【编辑】打开）
// ---------------------------------------------------------------------------

const editorEntry = ref<FileEntry | null>(null)
const editorShow = ref(false)
/** 编辑器抽屉组件实例：用于查询 dirty 状态并复用其关闭/放弃确认弹窗 */
const editorRef = ref<InstanceType<typeof FileEditorDrawer> | null>(null)

async function openEditor(entry: FileEntry) {
  // 编辑器已打开且存在未保存修改时，先确认（保存并继续 / 放弃 / 取消）再切换文件
  if (editorRef.value?.dirty && !(await editorRef.value.confirmClose())) {
    return
  }
  editorEntry.value = entry
  editorShow.value = true
}

/** 编辑器保存成功后刷新当前目录（节点大小/时间展示随之更新） */
async function onFileSaved() {
  await reload()
}

async function onRenameConfirm() {
  const target = renameTarget.value
  if (!target) return
  const nextName = renameValue.value.trim()
  if (!nextName) {
    message.warning('文件名不能为空')
    return
  }
  if (nextName === target.entry.name) {
    renameModalVisible.value = false
    return
  }
  if (target.workspaceId !== workspaceId.value) {
    renameModalVisible.value = false
    renameTarget.value = null
    message.warning('工作区已切换，请重新选择文件')
    return
  }

  renaming.value = true
  try {
    await renameFile(target.workspaceId, target.entry.path, nextName)
    renameModalVisible.value = false
    message.success(`已重命名为「${nextName}」`)
    await reload()
  } catch (err) {
    message.error(err instanceof Error ? err.message : '重命名失败')
  } finally {
    renaming.value = false
    renameTarget.value = null
  }
}

// ---------------------------------------------------------------------------
// 删除（右键菜单 → 输入名称二次确认；不可逆操作）
// ---------------------------------------------------------------------------

const deleteModalVisible = ref(false)
const deleteValue = ref('')
const deleting = ref(false)
const deleteTarget = ref<{ workspaceId: number; entry: FileEntry } | null>(null)

/** 弹窗显示状态变化：关闭时清理目标状态，避免残留指向旧条目 */
function onDeleteModalShow(show: boolean) {
  if (!show) {
    deleteTarget.value = null
    deleteValue.value = ''
  }
}

/** 输入名称与目标条目完全一致才允许确认（防手滑误删，VS Code 同款模式） */
const deleteConfirmDisabled = computed(
  () => deleteValue.value !== deleteTarget.value?.entry.name,
)

async function onDeleteConfirm() {
  const target = deleteTarget.value
  if (!target) return
  if (deleteConfirmDisabled.value) return
  if (target.workspaceId !== workspaceId.value) {
    deleteModalVisible.value = false
    deleteTarget.value = null
    message.warning('工作区已切换，请重新选择文件')
    return
  }

  deleting.value = true
  try {
    await deleteFile(target.workspaceId, target.entry.path)
    deleteModalVisible.value = false
    message.success(`已删除「${target.entry.name}」`)
    await reload()
  } catch (err) {
    message.error(err instanceof Error ? err.message : '删除失败')
  } finally {
    deleting.value = false
    deleteTarget.value = null
    deleteValue.value = ''
  }
}

// ---------------------------------------------------------------------------
// 条目交互：文件夹单击进入；文件单击图片预览 / 双击编辑
// ---------------------------------------------------------------------------

function isImageEntry(e: FileEntry): boolean {
  const t = e.mimeType ?? ''
  return (
    t.startsWith('image/') ||
    /\.(png|jpe?g|gif|webp|bmp|svg|avif|ico)$/i.test(e.name)
  )
}

const previewEntry = ref<FileEntry | null>(null)
/** 预览直链（异步换取：12 小时资源 token，绑定 workspace+path，不进日志泄露主 token） */
const previewSrc = ref('')
/** 隐藏的 n-image anchor：负责承载预览源，程序化触发原生预览（naive-ui expose showPreview） */
const previewImgRef = ref<{ showPreview: () => void } | null>(null)

/**
 * 为当前 previewEntry 换取短 token 直链；失败置空（图片预览留白，不阻断其它操作）。
 * 每次预览都签发新 token（12h 过期后由后端内存懒清理），无需客户端缓存。
 */
async function refreshPreviewUrl() {
  const entry = previewEntry.value
  if (!entry || !workspaceId.value) {
    previewSrc.value = ''
    return
  }
  try {
    previewSrc.value = await fetchPreviewUrl(workspaceId.value, entry.path)
  } catch {
    previewSrc.value = ''
  }
}

// 切换预览目标或切换项目时，重新换取对应文件的直链
watch([previewEntry, workspaceId], () => {
  void refreshPreviewUrl()
})

/** 单击条目：文件夹进入；图片文件直接弹出 n-image 原生预览（不经过中间弹窗） */
async function onEntryClick(entry: FileEntry) {
  if (entry.isDir) {
    enterDir(entry)
    return
  }
  if (!isImageEntry(entry)) return
  previewEntry.value = entry
  // 等短 token 直链就绪后再触发预览，保证预览层能取到图
  await refreshPreviewUrl()
  previewImgRef.value?.showPreview()
}

/** 双击条目：仅常见文本文件打开编辑器（二进制文件不响应，与右键菜单一致） */
function onEntryDblClick(entry: FileEntry) {
  if (!entry.isDir && isEditableFileName(entry.name)) void openEditor(entry)
}

// ---------------------------------------------------------------------------
// 拖拽上传：单目录模式下目标固定为「当前目录」
// ---------------------------------------------------------------------------

const dragActive = ref(false)

const uploading = ref(false)
const uploadProgress = ref(0)
const uploadingName = ref('')

/** 拖拽悬停：只做容器高亮；上传目标固定为当前目录，不再按悬停节点区分 */
function onDragOver() {
  dragActive.value = true
}

function onDragLeave(e: DragEvent) {
  // 只有真正离开容器才收起提示（relatedTarget 在容器外）
  const related = e.relatedTarget as Node | null
  const container = e.currentTarget as HTMLElement
  if (!container.contains(related)) {
    dragActive.value = false
  }
}

async function onDrop(e: DragEvent) {
  dragActive.value = false
  const files = Array.from(e.dataTransfer?.files ?? [])
  if (!files.length || !workspaceId.value || uploading.value) return
  await doUpload(files, currentDir.value)
}

/**
 * 执行上传：图片逐个串行压缩（避免并行解码多张大图撑爆内存），随后顺序上传。
 * 单个文件上传原子成功/失败；批次中途失败时已落盘文件保留，最后统一汇总提示。
 */
async function doUpload(files: File[], dir: string) {
  // 数量上限统一约束（粘贴/拖拽）：超过则拒绝整批，避免部分成功造成困惑
  if (files.length > MAX_UPLOAD_FILES) {
    message.warning(`一次最多上传 ${MAX_UPLOAD_FILES} 个文件，请分批操作`)
    return
  }
  uploading.value = true
  uploadProgress.value = 0
  const ok: string[] = []
  const failed: string[] = []
  try {
    const total = files.length
    let done = 0
    for (const file of files) {
      uploadingName.value = file.name
      try {
        const prepared = await prepareFile(file)
        const uploaded = await uploadFiles(workspaceId.value, dir, [prepared], (p) => {
          uploadProgress.value = (done + p) / total
        })
        done += 1
        ok.push(...uploaded.map((u) => u.name))
      } catch (err) {
        done += 1
        failed.push(`${file.name}（${err instanceof Error ? err.message : '未知错误'}）`)
      }
    }
    if (ok.length) message.success(`已上传 ${ok.length} 个文件`)
    if (failed.length) message.error(`上传失败 ${failed.length} 个：${failed.join('；')}`)
    await reload()
  } finally {
    uploading.value = false
    uploadProgress.value = 0
    uploadingName.value = ''
  }
}

// ---------------------------------------------------------------------------
// 粘贴上传：Ctrl/Cmd+V 在列表区域粘贴文件/截图，上传到当前目录
// ---------------------------------------------------------------------------

/** 列表容器 ref：点击列表区域时聚焦容器，使 paste 事件能落到容器上 */
const listRef = ref<HTMLElement | null>(null)

/** 列表区域粘贴：提取文件后上传到当前目录（行为与拖拽一致） */
async function onPaste(e: ClipboardEvent) {
  // 焦点在输入框等可编辑元素内时不拦截，文本粘贴照常工作
  if (isEditableTarget(e.target)) return
  const files = extractPastedFiles(e)
  if (!files.length || !workspaceId.value || uploading.value) return

  // 不支持的浏览器（如 Safari）：仅当确实提取到文件（截图场景）才提示；
  // 文件管理器粘贴在 Safari 中剪贴板不可见、无法检测，只能靠拖拽兜底
  if (!isPasteSupported()) {
    e.preventDefault()
    message.warning('当前浏览器不支持粘贴上传，请使用 Chrome/Edge/Firefox，或直接拖拽上传')
    return
  }

  e.preventDefault()
  await doUpload(files, currentDir.value)
}

/** 点击列表区域时聚焦容器：paste 事件派发给焦点元素，须让焦点落在容器上 */
function onListMouseDown(e: MouseEvent) {
  const t = e.target as HTMLElement | null
  if (t && typeof t.closest === 'function' && t.closest('input, textarea, button, [contenteditable="true"]')) {
    return
  }
  listRef.value?.focus({ preventScroll: true })
}

</script>

<template>
  <div class="relative flex h-full min-h-0 flex-col">
    <n-empty
      v-if="!workspaceId"
      class="mt-16"
      size="small"
      description="请先在左侧添加项目"
    />

    <template v-else>
      <!-- 路径输入栏：锁定根目录前缀 + 可编辑相对路径 + 进入/刷新图标按钮 -->
      <div class="flex items-center gap-1 px-3 pb-2 pt-2">
        <n-input
          v-model:value="pathInput"
          size="small"
          placeholder="path"
          :disabled="listLoading"
          clearable
          @keydown.enter.prevent="enterPath"
        >
          <template #prefix>
            <span class="shrink-0 text-xs text-ink-secondary">{{ wsBaseName }}/</span>
          </template>
        </n-input>
        <n-button
          quaternary
          circle
          size="small"
          title="进入"
          :disabled="listLoading"
          @click="enterPath"
        >
          <template #icon>
            <n-icon><ArrowForwardOutline /></n-icon>
          </template>
        </n-button>
        <n-button
          quaternary
          circle
          size="small"
          title="刷新"
          :loading="listLoading"
          @click="reload"
        >
          <template #icon>
            <n-icon><RefreshOutline /></n-icon>
          </template>
        </n-button>
      </div>

      <!-- 文件列表（拖拽/粘贴上传目标容器；右键条目弹菜单）。
           tabindex="-1" + 点击聚焦：paste 事件派发给焦点元素，需让焦点落在
           容器上才能接收 Ctrl/Cmd+V；outline-none 去掉焦点框避免视觉干扰 -->
      <div
        ref="listRef"
        tabindex="-1"
        class="min-h-0 flex-1 overflow-auto px-1 outline-none"
        @dragenter.prevent
        @dragover.prevent="onDragOver"
        @dragleave="onDragLeave"
        @drop.prevent="onDrop"
        @paste="onPaste"
        @mousedown="onListMouseDown"
      >
        <n-spin :show="listLoading">
          <!-- 返回上级：列表顶部 `...`（根目录不显示） -->
          <button
            v-if="canGoUp"
            class="flex w-full cursor-pointer items-center gap-2 rounded px-2.5 py-1 text-left hover:bg-surface-hover"
            title="返回上级"
            @click="goUp"
          >
            <!-- `...` 句号 glyph 在字体中位于基线以下，垂直居中后视觉偏下，上移 4px 修正 -->
            <span class="-translate-y-[4px] text-sm leading-none tracking-[0.25em] text-ink-secondary">...</span>
          </button>

          <!-- 空目录 -->
          <div
            v-if="!listLoading && entries.length === 0"
            class="px-3 py-8 text-center text-xs text-ink-muted"
          >
            当前目录为空
          </div>

          <!-- 条目：文件夹单击进入；文件单击图片预览 / 双击编辑 / 右键菜单 -->
          <div
            v-for="entry in entries"
            :key="entry.path"
            class="flex min-w-0 cursor-pointer items-center gap-1.5 rounded px-2.5 py-1 text-sm hover:bg-surface-hover"
            @click="onEntryClick(entry)"
            @dblclick="onEntryDblClick(entry)"
            @contextmenu.prevent="onEntryContextMenu($event, entry)"
          >
            <n-icon
              class="shrink-0"
              size="15"
              :class="entry.isDir ? 'text-amber-500' : 'text-ink-muted'"
            >
              <FolderOutline v-if="entry.isDir" />
              <ImageOutline v-else-if="isImageEntry(entry)" />
              <DocumentOutline v-else />
            </n-icon>
            <span class="truncate">{{ entry.name }}</span>
          </div>
        </n-spin>
      </div>

      <!-- 拖拽悬停提示：上传目标 = 当前目录 -->
      <div
        v-if="dragActive"
        class="pointer-events-none absolute inset-x-1 bottom-1 top-12 z-10 flex items-center justify-center rounded border-2 border-dashed border-blue-400 bg-blue-50/70 text-sm text-blue-600 dark:border-blue-500/50 dark:bg-blue-500/15 dark:text-blue-400"
      >
        <span>
          {{ currentDir ? `上传到「${currentDir}」目录` : '上传到项目根目录' }}
        </span>
      </div>

      <!-- 上传进度 -->
      <div v-if="uploading" class="px-3 pb-2">
        <n-progress
          type="line"
          :percentage="Math.round(uploadProgress * 100)"
          :show-indicator="false"
          :height="6"
          :processing="uploadProgress < 1"
        />
        <div class="mt-1 truncate text-xs text-ink-muted">正在上传 {{ uploadingName }}…</div>
      </div>
    </template>

    <!-- 右键菜单（manual 定位到鼠标位置） -->
    <n-dropdown
      trigger="manual"
      placement="bottom-start"
      :show="ctxMenuShow"
      :x="ctxMenuX"
      :y="ctxMenuY"
      :options="ctxOptions"
      @select="onCtxSelect"
      @clickoutside="ctxMenuShow = false"
    />

    <!-- 重命名弹窗：只提交新名称，后端负责工作区边界与文件名校验 -->
    <n-modal v-model:show="renameModalVisible" preset="card" title="重命名" style="width: 420px">
      <n-input
        v-model:value="renameValue"
        placeholder="请输入新名称"
        :disabled="renaming"
        clearable
        @keydown.enter.prevent="onRenameConfirm"
      />
      <template #footer>
        <n-space justify="end">
          <n-button quaternary :disabled="renaming" @click="renameModalVisible = false">
            取消
          </n-button>
          <n-button type="primary" :loading="renaming" @click="onRenameConfirm">
            确认
          </n-button>
        </n-space>
      </template>
    </n-modal>
    <!-- 删除确认弹窗：必须输入与目标完全一致的名称才可确认（不可逆操作） -->
    <n-modal
      v-model:show="deleteModalVisible"
      preset="card"
      title="确认删除"
      style="width: 420px"
      @update:show="onDeleteModalShow"
    >
      <div class="mb-3 space-y-2 text-sm text-ink-secondary">
        <p>
          即将<span class="font-medium text-red-500">永久删除</span>「{{ deleteTarget?.entry.name }}」
          <template v-if="deleteTarget?.entry.isDir">（含其下所有内容）</template>，此操作不可恢复。
        </p>
        <p>请输入名称以确认：</p>
      </div>
      <n-input
        v-model:value="deleteValue"
        :placeholder="`请输入 ${deleteTarget?.entry.name ?? ''} 确认删除`"
        :disabled="deleting"
        clearable
        @keydown.enter.prevent="onDeleteConfirm"
      />
      <template #footer>
        <n-space justify="end">
          <n-button quaternary :disabled="deleting" @click="deleteModalVisible = false">
            取消
          </n-button>
          <n-button
            type="error"
            :disabled="deleteConfirmDisabled"
            :loading="deleting"
            @click="onDeleteConfirm"
          >
            确认删除
          </n-button>
        </n-space>
      </template>
    </n-modal>

    <!--
      图片预览 anchor：隐藏的 n-image 承载预览源，点击图片文件时程序化调用
      showPreview() 直接进入原生预览（全屏遮罩 + 缩放工具条）。
      移出视口放置：预览弹层 teleport 到 body，不受 anchor 位置影响。
    -->
    <n-image
      ref="previewImgRef"
      :src="previewSrc"
      class="pointer-events-none fixed -left-[9999px] top-0"
      style="width: 1px; height: 1px"
    />

    <!-- 文本编辑器抽屉（双击文件 / 右键【编辑】打开；保存后刷新列表） -->
    <FileEditorDrawer
      ref="editorRef"
      v-model:show="editorShow"
      :workspace-id="workspaceId"
      :entry="editorEntry"
      @saved="onFileSaved"
    />
  </div>
</template>
