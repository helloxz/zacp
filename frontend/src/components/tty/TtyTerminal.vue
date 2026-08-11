<script setup lang="ts">
import { FitAddon } from '@xterm/addon-fit'
import { Terminal } from '@xterm/xterm'
import { nextTick, onBeforeUnmount, onMounted, ref, shallowRef, watch } from 'vue'
import { useAppStore } from '@/stores/app'
import { useTtySocket } from '@/composables/useTtySocket'
import type { TtyServerMessage, TtyTabStatus } from '@/types/tty'

const props = defineProps<{
  workspaceId: number
  tabId: string
  active: boolean
}>()

const emit = defineEmits<{
  (event: 'status', status: TtyTabStatus): void
  (event: 'ready', terminalId: string): void
  (event: 'exit', code: number): void
  (event: 'error', message: string): void
}>()

const appStore = useAppStore()
const container = ref<HTMLElement | null>(null)
const terminal = shallowRef<Terminal | null>(null)
const fitAddon = shallowRef<FitAddon | null>(null)
let resizeObserver: ResizeObserver | null = null
let disposed = false
let finalStatus: 'exited' | 'error' | null = null
const terminalDisposables: Array<{ dispose: () => void }> = []

function cssColor(name: string, fallback: string): string {
  const target = container.value ?? document.documentElement
  return getComputedStyle(target).getPropertyValue(name).trim() || fallback
}

function currentTheme() {
  const dark = appStore.isDark
  const background = cssColor('--color-surface', dark ? '#0f172a' : '#f8fafc')
  const foreground = cssColor('--color-ink', dark ? '#f1f5f9' : '#0f172a')
  const cursor = cssColor('--color-primary', dark ? '#38bdf8' : '#0ea5e9')
  const selectionBackground = cssColor('--color-surface-active', dark ? 'rgba(148, 163, 184, 0.2)' : 'rgba(226, 232, 240, 0.7)')

  return dark
    ? {
        background,
        foreground,
        cursor,
        cursorAccent: background,
        selectionBackground,
        black: '#0f172a',
        brightBlack: '#64748b',
        blue: '#38bdf8',
        brightBlue: '#7dd3fc',
        cyan: '#22d3ee',
        brightCyan: '#67e8f9',
        green: '#4ade80',
        brightGreen: '#86efac',
        magenta: '#e879f9',
        brightMagenta: '#f0abfc',
        red: '#f87171',
        brightRed: '#fca5a5',
        white: '#e2e8f0',
        brightWhite: '#f8fafc',
        yellow: '#facc15',
        brightYellow: '#fde047',
      }
    : {
        background,
        foreground,
        cursor,
        cursorAccent: background,
        selectionBackground,
        black: '#0f172a',
        brightBlack: '#64748b',
        blue: '#0284c7',
        brightBlue: '#0369a1',
        cyan: '#0891b2',
        brightCyan: '#0e7490',
        green: '#16a34a',
        brightGreen: '#15803d',
        magenta: '#c026d3',
        brightMagenta: '#a21caf',
        red: '#dc2626',
        brightRed: '#b91c1c',
        white: '#e2e8f0',
        brightWhite: '#f8fafc',
        yellow: '#ca8a04',
        brightYellow: '#fde047',
      }
}

function applyTheme() {
  if (terminal.value) {
    terminal.value.options.theme = currentTheme()
  }
}

function fit() {
  if (!props.active || !container.value || !fitAddon.value || !terminal.value) return
  const rect = container.value.getBoundingClientRect()
  if (rect.width <= 0 || rect.height <= 0) return
  fitAddon.value.fit()
}

function focus() {
  terminal.value?.focus()
}

function binaryStringToBytes(data: string): Uint8Array {
  const bytes = new Uint8Array(data.length)
  for (let index = 0; index < data.length; index += 1) {
    bytes[index] = data.charCodeAt(index) & 0xff
  }
  return bytes
}

function handleServerMessage(message: TtyServerMessage) {
  if (message.type === 'ready') {
    emit('status', 'connected')
    emit('ready', message.terminalId)
    void nextTick(fit)
    return
  }
  if (message.type === 'exit') {
    finalStatus = 'exited'
    emit('status', 'exited')
    emit('exit', message.code)
    return
  }
  finalStatus = 'error'
  emit('status', 'error')
  emit('error', message.message)
}

const socket = useTtySocket(props.workspaceId, {
  onStatus: (status) => {
    if (status === 'connecting') emit('status', 'connecting')
    if (status === 'closed' && !disposed && !finalStatus) emit('status', 'closed')
  },
  onOutput: (data) => {
    terminal.value?.write(data)
  },
  onMessage: handleServerMessage,
  onError: (message) => {
    if (!disposed) {
      finalStatus = 'error'
      emit('status', 'error')
      emit('error', message)
    }
  },
  onClose: () => {
    if (!disposed && !finalStatus) emit('status', 'closed')
  },
})

function handleResize(size: { cols: number; rows: number }) {
  socket.sendResize(size.cols, size.rows)
}

onMounted(() => {
  const instance = new Terminal({
    cursorBlink: true,
    convertEol: false,
    fontFamily: "ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', 'Courier New', monospace",
    fontSize: 14,
    lineHeight: 1.2,
    scrollback: 5000,
    theme: currentTheme(),
  })
  const addon = new FitAddon()
  terminal.value = instance
  fitAddon.value = addon
  instance.loadAddon(addon)
  if (!container.value) return
  instance.open(container.value)

  terminalDisposables.push(
    instance.onData((data) => {
      socket.sendInput(new TextEncoder().encode(data))
    }),
    instance.onBinary((data) => {
      socket.sendInput(binaryStringToBytes(data))
    }),
    instance.onResize(handleResize),
  )
  resizeObserver = new ResizeObserver(() => fit())
  resizeObserver.observe(container.value)
  emit('status', 'connecting')
  void socket.connect()
})

watch(
  () => [props.active, appStore.themeMode] as const,
  ([active]) => {
    applyTheme()
    if (active) void nextTick(fit)
  },
)

onBeforeUnmount(() => {
  disposed = true
  socket.close()
  resizeObserver?.disconnect()
  resizeObserver = null
  for (const disposable of terminalDisposables) disposable.dispose()
  terminalDisposables.length = 0
  fitAddon.value?.dispose()
  terminal.value?.dispose()
  fitAddon.value = null
  terminal.value = null
})

defineExpose({ focus, fit, close: socket.close })
</script>

<template>
  <div
    ref="container"
    class="h-full min-h-0 w-full overflow-hidden bg-surface p-2"
    @click="focus"
  />
</template>
