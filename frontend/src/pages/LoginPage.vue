<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { useMessage } from 'naive-ui'
import { useAuthStore } from '@/stores/auth'
import { connect as acpConnect } from '@/composables/useAcpSocket'
import { ApiError } from '@/api/types'
import { fetchCaptcha } from '@/api'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const message = useMessage()
const authStore = useAuthStore()

// 用户名/密码留空由用户手动输入，不做任何预填：
const username = ref('')
const password = ref('')
const submitting = ref(false)
const errorMsg = ref('')

// 图形验证码（仅认证启用时展示）
const captchaId = ref('')
const captchaImage = ref('')
const captchaCode = ref('')
const captchaLoading = ref(false)

async function refreshCaptcha() {
  if (!authStore.enabled) return
  captchaLoading.value = true
  try {
    const res = await fetchCaptcha()
    captchaId.value = res.id
    captchaImage.value = res.image
  } catch {
    // 静默：验证码加载失败不阻断登录，下次提交会提示刷新
  } finally {
    captchaLoading.value = false
  }
}

onMounted(async () => {
  await authStore.ensureStatus()
  if (authStore.enabled) {
    refreshCaptcha()
  }
})

watch(
  () => authStore.enabled,
  (v) => {
    if (v && !captchaImage.value) {
      refreshCaptcha()
    }
  },
)

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
  if (authStore.enabled) {
    const c = captchaCode.value.trim()
    if (!c) {
      errorMsg.value = t('login.captchaRequired')
      return
    }
    // 前端优先校验格式：4位字母数字，减轻无效后端请求
    if (c.length !== 4 || !/^[A-Za-z0-9]{4}$/.test(c)) {
      errorMsg.value = t('login.captchaInvalid')
      return
    }
  }
  submitting.value = true
  errorMsg.value = ''
  try {
    await authStore.login(
      uname,
      password.value,
      captchaId.value || undefined,
      captchaCode.value.trim() || undefined,
    )
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
    if (e instanceof ApiError) {
      if (e.code === 'captcha_required') {
        errorMsg.value = t('login.captchaRequired')
      } else if (e.code === 'captcha_invalid') {
        errorMsg.value = t('login.captchaInvalid')
      } else if (e.code === 'ip_blocked') {
        errorMsg.value = t('login.ipBlocked')
      } else if (e.code === 'invalid_credentials') {
        errorMsg.value = t('login.failed')
      } else {
        errorMsg.value = t('login.failed')
      }
    } else {
      errorMsg.value = t('login.failed')
    }
    // 验证码单次有效，失败后刷新并清空输入
    if (authStore.enabled) {
      captchaCode.value = ''
      refreshCaptcha()
    }
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
        <img
          src="/favicon.png"
          alt="Zacp"
          class="h-14 w-14 rounded-xl object-contain shadow-sm"
        />
        <h1 class="text-xl font-semibold text-gray-900 dark:text-gray-100">
          {{ t('login.title') }}
        </h1>
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

        <!-- 图形验证码（仅认证启用时展示） -->
        <div v-if="authStore.enabled" class="flex gap-2">
          <n-input
            v-model:value="captchaCode"
            :placeholder="t('login.captchaPlaceholder')"
            size="large"
            autocomplete="off"
            class="flex-1"
            @keydown.enter.prevent="handleSubmit"
          />
          <img
            v-if="captchaImage"
            :src="captchaImage"
            :alt="t('login.captcha')"
            :title="t('login.captchaRefresh')"
            class="h-[40px] w-[120px] cursor-pointer rounded border border-gray-200 object-cover dark:border-gray-700"
            @click="refreshCaptcha"
          />
          <div
            v-else
            class="flex h-[40px] w-[120px] cursor-pointer items-center justify-center rounded border border-gray-200 text-xs text-gray-400 dark:border-gray-700"
            @click="refreshCaptcha"
          >
            {{ captchaLoading ? '...' : t('login.captchaRefresh') }}
          </div>
        </div>

        <!-- 错误提示（密码错误 / 验证码错误 / IP 拉黑等） -->
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
