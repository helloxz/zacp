<script setup lang="ts">
import { CloseOutline, AddOutline } from '@vicons/ionicons5'
import { useI18n } from 'vue-i18n'
import type { TtyTabState } from '@/composables/useTtyManager'

const { t } = useI18n()
const props = defineProps<{
  tabs: TtyTabState[]
  activeTabId: string | null
  canCreate: boolean
}>()

const emit = defineEmits<{
  (event: 'select', id: string): void
  (event: 'create'): void
  (event: 'close', tab: TtyTabState): void
}>()

function statusClass(status: TtyTabState['status']): string {
  if (status === 'connected') return 'bg-emerald-500'
  if (status === 'error') return 'bg-rose-500'
  if (status === 'exited' || status === 'closed') return 'bg-slate-400'
  return 'bg-amber-400'
}
</script>

<template>
  <nav
    class="flex min-h-11 shrink-0 items-center gap-1 overflow-x-auto border-b border-divider bg-surface px-2"
    role="tablist"
    :aria-label="t('tty.title')"
  >
    <div
      v-for="tab in props.tabs"
      :key="tab.id"
      role="tab"
      tabindex="0"
      :aria-selected="tab.id === props.activeTabId"
      class="group flex h-8 min-w-28 max-w-52 cursor-pointer items-center gap-2 rounded-md px-2.5 text-xs transition-colors"
      :class="
        tab.id === props.activeTabId
          ? 'bg-surface-raised text-ink shadow-sm ring-1 ring-divider'
          : 'text-ink-muted hover:bg-surface-hover hover:text-ink'
      "
      @click="emit('select', tab.id)"
      @keydown.enter="emit('select', tab.id)"
      @keydown.space.prevent="emit('select', tab.id)"
    >
      <span class="h-1.5 w-1.5 shrink-0 rounded-full" :class="statusClass(tab.status)" />
      <span class="min-w-0 flex-1 truncate text-left">{{ tab.title }}</span>
      <n-button
        quaternary
        circle
        size="tiny"
        :aria-label="`${t('tty.close')} ${tab.title}`"
        class="opacity-60 group-hover:opacity-100"
        @click.stop="emit('close', tab)"
      >
        <template #icon>
          <n-icon size="13"><CloseOutline /></n-icon>
        </template>
      </n-button>
    </div>
    <n-button
      quaternary
      circle
      size="small"
      :disabled="!props.canCreate"
      :aria-label="t('tty.add')"
      :title="t('tty.add')"
      @click="emit('create')"
    >
      <template #icon>
        <n-icon><AddOutline /></n-icon>
      </template>
    </n-button>
  </nav>
</template>
