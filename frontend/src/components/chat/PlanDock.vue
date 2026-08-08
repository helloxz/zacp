<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { CheckmarkCircleOutline, ListOutline } from '@vicons/ionicons5'
import { NIcon } from 'naive-ui'
import PlanCard from '@/components/chat/PlanCard.vue'
import { useSessionStore } from '@/stores/session'
import type { Plan } from '@/types/ws'

const props = defineProps<{ sessionId: number }>()

const { t } = useI18n()
const sessionStore = useSessionStore()
const pinned = ref(false)

/** 实时 turn 优先，turn 结束或刷新后回退到最新 100 条历史消息中的最后计划。 */
const plan = computed<Plan | null>(
  () => sessionStore.activePlanOf(props.sessionId) ?? sessionStore.latestPlanOf(props.sessionId),
)

const entries = computed(() => plan.value?.entries ?? [])
const hasEntries = computed(() => entries.value.length > 0)
const allCompleted = computed(
  () => hasEntries.value && entries.value.every((entry) => entry.status === 'completed'),
)

const buttonClass = computed(() => {
  if (allCompleted.value) {
    return 'text-green-500 hover:bg-green-500/10 dark:text-green-400 dark:hover:bg-green-400/10'
  }
  if (hasEntries.value) {
    return 'text-primary hover:bg-sky-500/10 dark:hover:bg-sky-400/10'
  }
  return 'text-ink-muted hover:bg-surface-hover'
})

const buttonLabel = computed(() => {
  if (allCompleted.value) return t('plan.buttonCompleted')
  if (hasEntries.value) return t('plan.buttonActive')
  return t('plan.buttonEmpty')
})
</script>

<template>
  <div
    v-if="plan"
    class="group"
    @keydown.esc="pinned = false"
  >
    <button
      type="button"
      class="flex h-10 w-10 cursor-pointer items-center justify-center rounded-full border border-divider bg-surface-raised shadow-sm transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary/50"
      :class="buttonClass"
      :aria-label="buttonLabel"
      :aria-controls="`session-plan-panel-${sessionId}`"
      :aria-expanded="pinned"
      :title="buttonLabel"
      @click.stop="pinned = !pinned"
    >
      <NIcon size="20">
        <CheckmarkCircleOutline v-if="allCompleted" />
        <ListOutline v-else />
      </NIcon>
    </button>

    <div
      :id="`session-plan-panel-${sessionId}`"
      class="invisible absolute left-full top-1/2 z-30 -translate-y-1/2 pl-2 opacity-0 transition-[opacity,visibility] duration-150 group-hover:visible group-hover:opacity-100 group-focus-within:visible group-focus-within:opacity-100"
      :class="pinned ? 'visible opacity-100' : ''"
      @click.stop
    >
      <PlanCard
        :plan="plan"
        class="max-h-80 w-72 max-w-[calc(100vw-4rem)] overflow-y-auto"
      />
    </div>
  </div>
</template>
