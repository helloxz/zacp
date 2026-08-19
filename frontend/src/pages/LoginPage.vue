<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { useMessage } from 'naive-ui'
import { useAuthStore } from '@/stores/auth'
import { connect as acpConnect } from '@/composables/useAcpSocket'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const message = useMessage()
const authStore = useAuthStore()

// 用户名/密码留空由用户手动输入，不做任何预填：
// 免认证的 /auth/status 已不回传用户名，登录页也不从 store 回填，
// 避免把登录用户名暴露给浏览器自动填充或第三方。
const username = ref('')
const password = ref('')
const submitting = ref(false)
const errorMsg = ref('')

/** 登录成功后回跳地址（守卫写入的 redirect；默认首页） */
const redirectTo = computed(
  () => (typeof route.query.redirect === 'string' ? route.query.redirect : '/'),
)

async function handleSubmit() {
  if (submitting.value) {
    return
  }
  const uname = username.value.trim()
  if (!uname) {
    errorMsg.value = t('login.usernameRequired')
    return
  }
  if (!password.value) {
    errorMsg.value = t('login.passwordRequired')
    return
  }
  submitting.value = true
  errorMsg.value = ''
  try {
    await authStore.login(uname, password.value)
    message.success(t('login.success'))
    // 登录后恢复主通道连接（未登录时 useAcpSocket 会跳过建连）
    acpConnect()
    // 回跳原地址；redirect 为绝对外链时忽略（防开放重定向）
    if (redirectTo.value.startsWith('/')) {
      await router.replace(redirectTo.value)
    } else {
      await router.replace('/')
    }
  } catch (e) {
    errorMsg.value = t('login.failed')
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <div
    class="flex min-h-screen items-center justify-center bg-gray-50 px-4 dark:bg-gray-950"
  >
    <div
      class="w-full max-w-sm rounded-2xl border border-gray-200 bg-white p-8 shadow-sm dark:border-gray-800 dark:bg-gray-900"
    >
      <!-- Logo 区 -->
      <div class="mb-8 flex flex-col items-center gap-3">
        <div
          class="flex h-14 w-14 items-center justify-center rounded-xl bg-sky-500 text-xl font-bold text-white shadow-sm"
        >
          z
        </div>
        <h1 class="text-xl font-semibold text-gray-900 dark:text-gray-100">
          {{ t('login.title') }}
        </h1>
        <p class="text-sm text-gray-500 dark:text-gray-400">
          {{ t('login.subtitle') }}
        </p>
      </div>

      <form class="flex flex-col gap-4" @submit.prevent="handleSubmit">
        <n-input
          v-model:value="username"
          :placeholder="t('login.username')"
          size="large"
          autocomplete="username"
          @keydown.enter.prevent="handleSubmit"
        />
        <n-input
          v-model:value="password"
          type="password"
          show-password-on="click"
          :placeholder="t('login.password')"
          size="large"
          autocomplete="current-password"
          @keydown.enter.prevent="handleSubmit"
        />

        <!-- 错误提示（密码错误等） -->
        <p v-if="errorMsg" class="text-sm text-red-500">{{ errorMsg }}</p>

        <n-button
          type="primary"
          size="large"
          block
          attr-type="submit"
          :loading="submitting"
        >
          {{ t('login.submit') }}
        </n-button>
      </form>
    </div>
  </div>
</template>
