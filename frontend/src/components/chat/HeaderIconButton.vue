<script setup lang="ts">
import { computed } from 'vue'
import { NButton, NIcon } from 'naive-ui'
import { useAppStore } from '@/stores/app'

/**
 * 会话头部右侧的图标按钮：统一尺寸的浅色圆角底壳（无边框）+ 灰色图标。
 *
 * 三个按钮（Web TTY / 本地工具 / 侧边面板）共用同一壳样式，解决不同图标
 * （终端横向矩形、打开方形框、chevron 窄三角）视觉重量不一致导致的
 * 「看起来大小不一」问题——用相同大小的底壳把光学尺寸对齐。
 *
 * 亮/暗两套背景由 token 自动切换（surface-hover / surface-active），
 * hover 仅加深底色，不出现突兀的背景色块。
 */
const props = defineProps<{
  title: string
  disabled?: boolean
}>()

const emit = defineEmits<{ click: [event: MouseEvent] }>()

const appStore = useAppStore()

/** 图标颜色：纯灰、无 hover 背景（不用 quaternary/primary）；暗色下换亮一档 */
const iconTheme = computed(() =>
  appStore.isDark
    ? {
        textColor: '#64748b', // slate-500
        textColorHover: '#94a3b8', // slate-400
        textColorPressed: '#94a3b8',
        textColorFocus: '#94a3b8',
      }
    : {
        textColor: '#94a3b8', // slate-400
        textColorHover: '#475569', // slate-600：hover 仅加深灰色
        textColorPressed: '#475569',
        textColorFocus: '#475569',
      },
)
</script>

<template>
  <div
    class="flex h-6 w-6 shrink-0 items-center justify-center rounded-md bg-surface-hover transition-colors hover:bg-surface-active"
  >
    <n-button
      text
      circle
      size="small"
      :theme-overrides="iconTheme"
      :title="props.title"
      :aria-label="props.title"
      :disabled="props.disabled"
      @click="emit('click', $event)"
    >
      <template #icon>
        <!-- 统一图标字号（n-button small 默认 14px），保证三个图标在壳内视觉一致 -->
        <n-icon class="text-[15px]"><slot /></n-icon>
      </template>
    </n-button>
  </div>
</template>
