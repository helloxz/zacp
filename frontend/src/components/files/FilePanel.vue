<script setup lang="ts">
/**
 * FilePanel — 右侧信息栏：信息 | 文件 | Git 三个 Tab。
 * 目前实现「信息」（SessionInfo）、「文件」（FileExplorer）与按需加载的「Git」（GitPanel）。
 */
import { ref } from 'vue'
import FileExplorer from '@/components/files/FileExplorer.vue'
import GitPanel from '@/components/files/GitPanel.vue'
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
      class="min-h-0 flex-1 px-3 pt-1.5"
    >
      <n-tab-pane class="min-h-0 flex-1" name="info" tab="信息">
        <SessionInfo />
      </n-tab-pane>

      <n-tab-pane class="min-h-0 flex-1" name="files" tab="文件">
        <FileExplorer />
      </n-tab-pane>

      <n-tab-pane class="min-h-0 flex-1" name="git" tab="Git" display-directive="if">
        <GitPanel />
      </n-tab-pane>
    </n-tabs>
  </aside>
</template>
