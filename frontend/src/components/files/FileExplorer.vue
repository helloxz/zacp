<script setup lang="ts">
/**
 * FileExplorer — 「文件」Tab 的文件树。
 *
 * 功能：
 * - 懒加载文件树（受控展开，展开时才拉取子目录）
 * - 拖拽上传：拖到目录节点 = 上传到该目录；拖到空白区 = 上传到工作区根
 * - 图片自动压缩转 webp（等比不裁剪，>5MB 降采样兜底），非图片原样直传
 * - 点击图片文件 → 直接调用 Naive UI n-image 原生预览（全屏遮罩+缩放工具条，
 *   走后端 raw 接口，天然受 workspace 边界保护）
 * - 右键节点 → 「重命名 / 复制名称」菜单（无 https 环境下复制回退）
 *
 * 数据根 = 当前会话所属 workspace；无会话时用默认 workspace。
 */
import { computed, h, onMounted, ref, watch } from 'vue'
import {
  NIcon,
  useMessage,
  type DropdownOption,
  type TreeOption,
} from 'naive-ui'
import {
  FolderOutline,
  DocumentOutline,
  ImageOutline,
  RefreshOutline,
} from '@vicons/ionicons5'
import { fetchFiles, fetchPreviewUrl, renameFile, uploadFiles } from '@/api'
import type { FileEntry } from '@/types/models'
import { useSessionStore } from '@/stores/session'
import { useAppStore } from '@/stores/app'
import { copyText } from '@/utils/clipboard'
import FileEditorDrawer from '@/components/files/FileEditorDrawer.vue'

/** 文件树节点：key 用相对路径（后端约定 `/` 分隔），raw 存原始条目 */
interface FileTreeNode extends TreeOption {
  raw: FileEntry
}

/** 图片压缩后大小上限（与后端 5MB 分档一致，保证后端不会拒绝） */
const MAX_IMAGE_BYTES = 5 * 1024 * 1024

const message = useMessage()
const sessionStore = useSessionStore()
const appStore = useAppStore()

/** 项目名 tooltip 主题：浅色白底浅字；暗色下跟随 Naive 主题（默认深色底） */
const wsTooltipTheme = computed(() =>
  appStore.isDark
    ? {}
    : {
        color: '#fff',
        textColor: '#333',
        boxShadow: '0 2px 8px rgba(0, 0, 0, 0.12)',
      },
)

/** 当前 workspace：优先当前会话所属项目，其次默认项目 */
const activeWs = computed(() => {
  const sid = sessionStore.activeSession?.workspace?.id
  if (sid) {
    return sessionStore.workspaces.find((w) => w.id === sid)
  }
  return sessionStore.defaultWorkspace()
})
const workspaceId = computed(() => activeWs.value?.id ?? 0)

const treeData = ref<FileTreeNode[]>([])
const expandedKeys = ref<string[]>([])
const treeLoading = ref(false)

const dragActive = ref(false)
/** 拖拽悬停的目录 key（null = 空白区 → 上传到根） */
const dragOverKey = ref<string | null>(null)

const uploading = ref(false)
const uploadProgress = ref(0)
const uploadingName = ref('')

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

// ---------------------------------------------------------------------------
// 右键菜单（重命名 / 复制名称）
// ---------------------------------------------------------------------------

const ctxMenuShow = ref(false)
const ctxMenuX = ref(0)
const ctxMenuY = ref(0)
const ctxMenuEntry = ref<FileEntry | null>(null)
/** 右键菜单：文件多一个「编辑」项（目录不可编辑） */
const ctxOptions = computed<DropdownOption[]>(() => {
  const options: DropdownOption[] = [
    { key: 'rename', label: '重命名' },
    { key: 'copy-name', label: '复制名称' },
  ]
  if (ctxMenuEntry.value && !ctxMenuEntry.value.isDir) {
    options.unshift({ key: 'edit', label: '编辑' })
  }
  return options
})
const renameModalVisible = ref(false)
const renameValue = ref('')
const renaming = ref(false)
const renameTarget = ref<{ workspaceId: number; entry: FileEntry } | null>(null)

/** 右键节点：定位目标条目并弹出菜单（路径从节点 DOM 的 data-dir 取，与拖拽同套路） */
function onContextMenu(e: MouseEvent) {
  const el = (e.target as HTMLElement).closest('.n-tree-node') as HTMLElement | null
  const dir = el?.dataset.dir
  if (dir == null) return
  const node = findNode(treeData.value, dir)
  if (!node) return
  ctxMenuEntry.value = node.raw
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
  if (key === 'rename') {
    renameTarget.value = { workspaceId: workspaceId.value, entry }
    renameValue.value = entry.name
    renameModalVisible.value = true
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

/** 编辑器保存成功后刷新整树（节点大小/时间展示随之更新） */
async function onFileSaved() {
  await reloadAll()
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
    // 重命名目录会使其子节点 key 全部变化，整树刷新可避免旧路径缓存残留。
    await reloadAll()
  } catch (err) {
    message.error(err instanceof Error ? err.message : '重命名失败')
  } finally {
    renaming.value = false
    renameTarget.value = null
  }
}

// ---------------------------------------------------------------------------
// 树数据：懒加载
// ---------------------------------------------------------------------------

function toNode(e: FileEntry): FileTreeNode {
  return { key: e.path, label: e.name, isLeaf: !e.isDir, raw: e }
}

async function buildChildren(dir: string): Promise<FileTreeNode[]> {
  // 隐藏文件由后端强制过滤（不提供开关），这里只拿可见条目
  const entries = await fetchFiles(workspaceId.value, dir)
  return entries.map(toNode)
}

/** 按 key 递归查找节点 */
function findNode(nodes: FileTreeNode[], key: string): FileTreeNode | null {
  for (const n of nodes) {
    if (n.key === key) return n
    if (n.children) {
      const r = findNode(n.children as FileTreeNode[], key)
      if (r) return r
    }
  }
  return null
}

async function loadRoot() {
  if (!workspaceId.value) return
  treeLoading.value = true
  try {
    treeData.value = await buildChildren('')
  } catch (err) {
    treeData.value = []
    message.error(err instanceof Error ? err.message : '加载文件列表失败')
  } finally {
    treeLoading.value = false
  }
}

/**
 * naive-ui 懒加载回调（on-load）：展开「未加载」（children 为 undefined）的目录时
 * 由 NTree 调用，返回的 Promise resolve 后自动结束 loading 状态并完成展开。
 * 必须在此给 node.children 赋值（NTree 以 children 有无判断 shallowLoaded）。
 * 加载失败按空目录处理，避免无限重试。
 */
async function onLoad(node: TreeOption) {
  const children = await buildChildren(String(node.key)).catch(() => [] as FileTreeNode[])
  node.children = children
  // 换引用强制 NTree 重新计算树结构（直接改 children 不保证触发重渲染）
  treeData.value = [...treeData.value]
}

/** 受控展开状态记录（实际加载交给 on-load，这里只维护 keys） */
function onExpandedKeys(keys: Array<string | number>) {
  expandedKeys.value = keys.map(String)
}

/** 刷新指定目录（上传成功后调用）；不在当前展开视图内的目录无需刷新 */
async function reloadDir(dir: string) {
  if (dir === '') {
    await loadRoot()
    return
  }
  const node = findNode(treeData.value, dir)
  if (!node) return
  node.children = await buildChildren(dir)
  treeData.value = [...treeData.value]
}

/** 整树重建（切换 workspace 时） */
async function reloadAll() {
  expandedKeys.value = []
  await loadRoot()
}

// workspace 变化 → 重建树（目录不同，缓存作废）；同时关闭可能指向旧 workspace 的编辑器
watch(workspaceId, async () => {
  // 有未保存修改时先征得用户同意（切换后旧文件无法再保存，故只提供「放弃/取消」）；
  // 用户取消 → 保持抽屉打开（内容仍可复制），但树照常切换到新 workspace
  if (editorRef.value?.dirty) {
    if (await editorRef.value.confirmDiscard()) {
      editorShow.value = false
    }
  } else {
    editorShow.value = false
  }
  void reloadAll()
})

onMounted(() => {
  void loadRoot()
})

// ---------------------------------------------------------------------------
// 拖拽上传
// ---------------------------------------------------------------------------

function isImageEntry(e: FileEntry): boolean {
  const t = e.mimeType ?? ''
  return (
    t.startsWith('image/') ||
    /\.(png|jpe?g|gif|webp|bmp|svg|avif|ico)$/i.test(e.name)
  )
}

/**
 * 节点 props：把真实路径挂到节点 DOM（naive-ui 非虚拟滚动树节点没有 data-key 属性，
 * 拖拽定位必须靠自定义 data 属性），拖拽时通过 dataset 取目标目录。
 * 注意：naive-ui 回调签名是 (info: { option }) => HTMLAttributes，参数包在对象里。
 */
function nodeProps({ option }: { option: TreeOption }): Record<string, unknown> {
  const e = (option as FileTreeNode).raw
  return {
    'data-dir': e.path,
    'data-isdir': e.isDir ? '1' : '0',
    // naive-ui NTree 没有 dblclick emit，双击只能通过节点 props 绑定原生事件；
    // 文本文件 → 打开编辑器（图片预览走 onSelect 选中路径，不冲突）
    onDblclick: () => {
      if (e && !e.isDir) {
        void openEditor(e)
      }
    },
  }
}

/** 拖拽悬停：高亮目标节点（路径从节点 DOM 的 data-dir 属性取） */
function onDragOver(e: DragEvent) {
  dragActive.value = true
  const el = (e.target as HTMLElement).closest('.n-tree-node') as HTMLElement | null
  dragOverKey.value = el?.dataset.dir ?? null
}

function onDragLeave(e: DragEvent) {
  // 只有真正离开容器才收起提示（relatedTarget 在容器外）
  const related = e.relatedTarget as Node | null
  const container = (e.currentTarget as HTMLElement)
  if (!container.contains(related)) {
    dragActive.value = false
    dragOverKey.value = null
  }
}

async function onDrop(e: DragEvent) {
  dragActive.value = false
  const files = Array.from(e.dataTransfer?.files ?? [])
  if (!files.length || !workspaceId.value || uploading.value) return

  // 目标目录：悬停在目录节点 = 该目录；悬停在文件节点 = 其父目录；空白区 = 根
  const el = (e.target as HTMLElement).closest('.n-tree-node') as HTMLElement | null
  const over = el?.dataset.dir ?? null
  let dir = ''
  if (over) {
    if (el?.dataset.isdir === '1') {
      dir = over
    } else {
      const i = over.lastIndexOf('/')
      dir = i >= 0 ? over.slice(0, i) : ''
    }
  }
  dragOverKey.value = null
  await doUpload(files, dir)
}

/**
 * 执行上传：图片逐个串行压缩（避免并行解码多张大图撑爆内存），随后顺序上传。
 * 单个文件上传原子成功/失败；批次中途失败时已落盘文件保留，最后统一汇总提示。
 */
async function doUpload(files: File[], dir: string) {
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
    if (ok.length) message.success(`已上传 ${ok.length} 个文件：${ok.join('、')}`)
    if (failed.length) message.error(`上传失败 ${failed.length} 个：${failed.join('；')}`)
    await reloadDir(dir)
  } finally {
    uploading.value = false
    uploadProgress.value = 0
    uploadingName.value = ''
  }
}

// ---------------------------------------------------------------------------
// 图片压缩（前端先行，后端 5MB 校验兜底）
// ---------------------------------------------------------------------------

/** 图片转 webp（等比不裁剪）：原尺寸 0.8 → 超限降采样最长边 2048 → 降质量 0.6 */
async function prepareFile(file: File): Promise<File> {
  // 非图片原样直传；SVG 是矢量图且通常很小，直传保留矢量与无损性
  if (!file.type.startsWith('image/') || file.type === 'image/svg+xml') return file
  try {
    const img = await loadImage(file)
    const { naturalWidth: w, naturalHeight: h } = img
    let blob = await encodeWebp(img, w, h, 0.8)
    if (blob.size > MAX_IMAGE_BYTES) {
      const scale = Math.min(1, 2048 / Math.max(w, h))
      blob = await encodeWebp(
        img,
        Math.max(1, Math.round(w * scale)),
        Math.max(1, Math.round(h * scale)),
        0.8,
      )
    }
    if (blob.size > MAX_IMAGE_BYTES) {
      const scale = Math.min(1, 2048 / Math.max(w, h))
      blob = await encodeWebp(
        img,
        Math.max(1, Math.round(w * scale)),
        Math.max(1, Math.round(h * scale)),
        0.6,
      )
    }
    // 压缩后仍超限：原样直传，由后端按图片 5MB 规则处理
    const name = file.name.replace(/\.[^.]+$/, '') + '.webp'
    return new File([blob], name, { type: 'image/webp' })
  } catch {
    return file // 解码/编码失败（损坏文件、heic 等）→ 原样上传
  }
}

function loadImage(file: File): Promise<HTMLImageElement> {
  return new Promise((resolve, reject) => {
    const url = URL.createObjectURL(file)
    const img = new Image()
    img.onload = () => {
      URL.revokeObjectURL(url)
      resolve(img)
    }
    img.onerror = () => {
      URL.revokeObjectURL(url)
      reject(new Error('图片解码失败'))
    }
    img.src = url
  })
}

function encodeWebp(
  img: HTMLImageElement,
  w: number,
  h: number,
  q: number,
): Promise<Blob> {
  return new Promise((resolve, reject) => {
    const canvas = document.createElement('canvas')
    canvas.width = w
    canvas.height = h
    const ctx = canvas.getContext('2d')
    if (!ctx) {
      reject(new Error('canvas 不可用'))
      return
    }
    ctx.drawImage(img, 0, 0, w, h)
    canvas.toBlob(
      (b) => (b ? resolve(b) : reject(new Error('webp 编码失败'))),
      'image/webp',
      q,
    )
  })
}

// ---------------------------------------------------------------------------
// 节点渲染 & 点击预览
// ---------------------------------------------------------------------------

function renderLabel({ option }: { option: TreeOption }) {
  const e = (option as FileTreeNode).raw
  const icon = e.isDir
    ? FolderOutline
    : isImageEntry(e)
      ? ImageOutline
      : DocumentOutline
  return h('div', { class: 'flex min-w-0 items-center gap-1.5' }, [
    h(NIcon, { size: 15 }, { default: () => h(icon) }),
    h('span', { class: 'truncate' }, option.label as string),
  ])
}

/** 点击节点：图片文件 → 直接弹出 n-image 原生预览（不经过中间弹窗） */
async function onSelect(keys: Array<string | number>) {
  if (!keys.length) return
  const node = findNode(treeData.value, String(keys[0]))
  const e = node?.raw
  if (e && !e.isDir && isImageEntry(e)) {
    previewEntry.value = e
    // 等短 token 直链就绪后再触发预览，保证预览层能取到图
    await refreshPreviewUrl()
    previewImgRef.value?.showPreview()
  }
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
      <!-- 工具栏：项目名（tooltip 展示完整路径）+ 刷新 -->
      <div class="flex items-center gap-2 px-3 pb-1">
        <n-tooltip
          class="min-w-0 flex-1"
          placement="left"
          :theme-overrides="wsTooltipTheme"
        >
          <template #trigger>
            <span class="block w-full truncate text-xs font-medium text-ink-secondary">
              {{ activeWs?.name || '项目文件' }}
            </span>
          </template>
          <span class="break-all">{{ activeWs?.path }}</span>
        </n-tooltip>
        <n-button
          quaternary
          circle
          size="tiny"
          title="刷新"
          :loading="treeLoading"
          @click="reloadAll"
        >
          <template #icon>
            <n-icon><RefreshOutline /></n-icon>
          </template>
        </n-button>
      </div>

      <!-- 文件树（拖拽目标容器；右键节点弹菜单） -->
      <div
        class="min-h-0 flex-1 overflow-auto px-1"
        @dragenter.prevent
        @dragover.prevent="onDragOver"
        @dragleave="onDragLeave"
        @drop.prevent="onDrop"
        @contextmenu.prevent="onContextMenu"
      >
        <n-tree
          block-line
          expand-on-click
          :data="treeData"
          :expanded-keys="expandedKeys"
          :node-props="nodeProps"
          :render-label="renderLabel"
          :on-load="onLoad"
          :selectable="true"
          @update:expanded-keys="onExpandedKeys"
          @update:selected-keys="onSelect"
        />
      </div>

      <!-- 拖拽悬停提示：目录节点高亮 + 上传目标说明 -->
      <div
        v-if="dragActive"
        class="pointer-events-none absolute inset-x-1 bottom-1 top-8 z-10 flex items-center justify-center rounded border-2 border-dashed border-blue-400 bg-blue-50/70 text-sm text-blue-600 dark:border-blue-500/50 dark:bg-blue-500/15 dark:text-blue-400"
      >
        <span>
          {{
            dragOverKey
              ? `上传到「${dragOverKey}」目录`
              : '拖到目录上可指定位置；此处为项目根目录'
          }}
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

    <!-- 文本编辑器抽屉（双击文件 / 右键【编辑】打开；保存后刷新树） -->
    <FileEditorDrawer
      ref="editorRef"
      v-model:show="editorShow"
      :workspace-id="workspaceId"
      :entry="editorEntry"
      @saved="onFileSaved"
    />
  </div>
</template>
