<script setup lang="ts">
/**
 * FilePanel — 右侧信息栏：信息 | 文件 | Git 三个 Tab。
 * 目前实现「信息」（SessionInfo）与「文件」（FileExplorer），「Git」留空占位。
 */
import { ref } from 'vue'
import FileExplorer from '@/components/files/FileExplorer.vue'
import SessionInfo from '@/components/files/SessionInfo.vue'

const tab = ref('info')
</script>

<template>
  <aside class="flex h-full w-80 shrink-0 flex-col border-l border-divider bg-surface-raised">
    <n-tabs
      v-model:value="tab"
      type="line"
      size="small"
      justify-content="space-evenly"
      class="shrink-0 px-3 pt-1.5"
    >
      <n-tab-pane name="info" tab="信息">
        <SessionInfo />
      </n-tab-pane>

      <n-tab-pane name="files" tab="文件">
        <FileExplorer />
      </n-tab-pane>

      <n-tab-pane name="git" tab="Git">
        <div class="flex h-full items-center justify-center">
          <n-empty size="small" description="Git Tab 待开发" />
        </div>
      </n-tab-pane>
    </n-tabs>
  </aside>
</template>
