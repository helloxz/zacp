<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  CloseOutline,
  InformationCircleOutline,
  ServerOutline,
  SettingsOutline,
} from '@vicons/ionicons5'
import AgentSettings from '@/components/shell/AgentSettings.vue'
import SystemSettings from '@/components/shell/SystemSettings.vue'
import AboutSettings from '@/components/shell/AboutSettings.vue'

/**
 * 设置弹窗：左侧菜单（智能体 / 系统设置 / 关于）+ 右侧内容区。
 * - 菜单选中为本地状态，每次打开默认定位第一个（智能体），不记忆位置；
 * - 内容组件用 KeepAlive 缓存，切换菜单不丢失表单草稿（如显示名输入）；
 * - 弹窗为自绘结构（非 preset card），便于控制圆角与布局。
 */
const props = defineProps<{ show: boolean }>()
const emit = defineEmits<{ (e: 'update:show', v: boolean): void }>()

const { t } = useI18n()

type MenuKey = 'agent' | 'system' | 'about'

/** 当前选中的菜单 key */
const activeKey = ref<MenuKey>('agent')

/** 左侧菜单项：key + 文案 + 图标 */
const menus = computed(() => [
  { key: 'agent' as const, label: t('settings.agent.title'), icon: ServerOutline },
  { key: 'system' as const, label: t('settings.system.title'), icon: SettingsOutline },
  { key: 'about' as const, label: t('settings.about.title'), icon: InformationCircleOutline },
])

/** 菜单 key → 内容组件映射 */
const views: Record<MenuKey, typeof AgentSettings> = {
  agent: AgentSettings,
  system: SystemSettings,
  about: AboutSettings,
}
</script>

<template>
  <n-modal
    :show="props.show"
    :mask-closable="true"
    @update:show="emit('update:show', $event)"
  >
    <div
      class="flex h-[560px] max-h-[85vh] w-[880px] max-w-[92vw] flex-col overflow-hidden rounded-2xl bg-white shadow-2xl"
    >
      <!-- 顶部标题栏 -->
      <header
        class="flex shrink-0 items-center justify-between border-b border-slate-100 px-6 py-4"
      >
        <h2 class="text-base font-semibold text-slate-800">
          {{ t('settings.title') }}
        </h2>
        <button
          type="button"
          :aria-label="t('settings.close')"
          class="flex h-8 w-8 cursor-pointer items-center justify-center rounded-full text-slate-400 transition-colors hover:bg-slate-100 hover:text-slate-600 focus:outline-none focus-visible:ring-2 focus-visible:ring-slate-300"
          @click="emit('update:show', false)"
        >
          <n-icon :size="18"><CloseOutline /></n-icon>
        </button>
      </header>

      <div class="flex min-h-0 flex-1">
        <!-- 左侧菜单 -->
        <nav
          class="flex w-44 shrink-0 flex-col gap-1 border-r border-slate-100 bg-slate-50/70 p-3"
        >
          <button
            v-for="item in menus"
            :key="item.key"
            type="button"
            class="flex cursor-pointer items-center gap-2.5 rounded-lg px-3 py-2.5 text-sm transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-indigo-300"
            :class="
              activeKey === item.key
                ? 'bg-white font-medium text-indigo-600 shadow-sm ring-1 ring-slate-200'
                : 'text-slate-600 hover:bg-white/70 hover:text-slate-900'
            "
            @click="activeKey = item.key"
          >
            <n-icon :size="18"><component :is="item.icon" /></n-icon>
            {{ item.label }}
          </button>
        </nav>

        <!-- 右侧内容区：随菜单切换，KeepAlive 保留组件状态 -->
        <div class="min-w-0 flex-1 overflow-y-auto p-6">
          <KeepAlive>
            <component :is="views[activeKey]" />
          </KeepAlive>
        </div>
      </div>
    </div>
  </n-modal>
</template>
