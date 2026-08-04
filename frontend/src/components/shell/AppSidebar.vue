<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { AddOutline, ChevronBackOutline, ChevronForwardOutline } from '@vicons/ionicons5'
import { NIcon } from 'naive-ui'
import SidebarSessionList from '@/components/shell/SidebarSessionList.vue'
import UserFooter from '@/components/shell/UserFooter.vue'

const props = defineProps<{ collapsed?: boolean }>()
const emit = defineEmits<{
  (e: 'toggle'): void
  (e: 'open-settings'): void
}>()

const { t } = useI18n()
const router = useRouter()

/**
 * 「新建会话」：回到空态（清空选中）。
 * 设计约定：首条消息发送时才真正创建会话，避免空会话垃圾（设计文档 §4.1）。
 */
function onNewSession() {
  void router.push({ name: 'home' })
}
</script>

<template>
  <!-- 侧栏折叠：260px ↔ 64px 图标条（PC 界面，不做移动端抽屉） -->
  <aside
    class="flex flex-col border-r border-slate-200 bg-slate-50 transition-[width] duration-200"
    :class="collapsed ? 'w-16' : 'w-[260px]'"
  >
    <!-- 收起态：仅保留「展开」按钮 -->
    <template v-if="collapsed">
      <button
        class="mx-auto mt-3 flex h-9 w-9 items-center justify-center rounded-lg text-slate-500 transition-colors hover:bg-slate-200/60 hover:text-slate-700"
        :aria-label="t('shell.expandSidebar')"
        :title="t('shell.expandSidebar')"
        @click="emit('toggle')"
      >
        <n-icon size="20"><ChevronForwardOutline /></n-icon>
      </button>
    </template>

    <!-- 展开态：新建会话 + 折叠按钮 + 列表 + 用户区 -->
    <template v-else>
      <div class="flex items-center gap-1 p-3">
        <n-button block secondary class="flex-1" @click="onNewSession">
          <template #icon>
            <n-icon><AddOutline /></n-icon>
          </template>
          {{ t('shell.newSession') }}
        </n-button>
        <n-button
          quaternary
          circle
          size="small"
          :aria-label="t('shell.collapseSidebar')"
          :title="t('shell.collapseSidebar')"
          @click="emit('toggle')"
        >
          <template #icon>
            <n-icon><ChevronBackOutline /></n-icon>
          </template>
        </n-button>
      </div>

      <SidebarSessionList class="min-h-0 flex-1 overflow-y-auto px-3 pb-4" />
      <UserFooter @open-settings="emit('open-settings')" />
    </template>
  </aside>
</template>
