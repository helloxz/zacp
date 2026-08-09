<script setup lang="ts">
import { computed, ref } from 'vue'
import { useDialog, useMessage } from 'naive-ui'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { updateCredentials } from '@/api'
import { useAuthStore } from '@/stores/auth'

/**
 * 用户设置：单用户登录保护的用户名/密码管理。
 *
 * 语义（与后端一致）：
 * - 密码留空并保存 = 清除密码、关闭登录保护（恢复无需登录）；
 * - 密码非空并保存 = 启用登录保护（需填用户名）；
 * - 保存后后端会吊销全部已签发 token：若新状态为「已启用」则本地登出并跳转登录页，
 *   若为「已关闭」则本地清 token、留在当前页（后端已放行所有请求）。
 */
const { t } = useI18n()
const message = useMessage()
const dialog = useDialog()
const router = useRouter()
const authStore = useAuthStore()

const username = ref(authStore.username)
const password = ref('')
const saving = ref(false)

/** 后端当前是否已启用登录保护（响应式：多 tab 期间状态变化后确认弹窗用最新值） */
const currentlyEnabled = computed(() => authStore.enabled)

async function doSave() {
  if (saving.value) {
    return
  }
  saving.value = true
  try {
    const status = await updateCredentials(username.value.trim(), password.value)
    authStore.applyStatus(status)
    authStore.forceLogout() // 后端已吊销全部 token，本地也必须失效
    if (status.enabled) {
      message.success(t('settings.user.reLogin'))
      await router.replace({ name: 'login', query: { redirect: '/' } })
    } else {
      message.success(t('settings.user.disabledNow'))
      // 保留输入框但清空密码，避免残留明文
      password.value = ''
    }
  } catch {
    message.error(t('settings.user.saveFailed'))
  } finally {
    saving.value = false
  }
}

function onSaveClick() {
  // 密码留空 = 关闭登录保护：若当前已启用，需二次确认（避免误操作）
  if (!password.value && currentlyEnabled) {
    dialog.warning({
      title: t('settings.user.confirmDisableTitle'),
      content: t('settings.user.confirmDisableContent'),
      positiveText: t('common.confirm'),
      negativeText: t('common.cancel'),
      onPositiveClick: () => doSave(),
    })
    return
  }
  void doSave()
}
</script>

<template>
  <div class="flex flex-col gap-6">
    <div>
      <h3 class="text-base font-semibold text-ink">{{ t('settings.user.title') }}</h3>
      <p class="mt-1 text-sm text-ink-muted">{{ t('settings.user.subtitle') }}</p>
    </div>

    <!-- 当前状态 -->
    <section class="flex flex-col gap-2">
      <h4 class="text-sm font-medium text-ink-secondary">{{ t('settings.user.statusLabel') }}</h4>
      <div
        class="flex items-center gap-2 text-sm"
        :class="authStore.enabled ? 'text-emerald-600 dark:text-emerald-400' : 'text-ink-muted'"
      >
        <span class="inline-block h-2 w-2 rounded-full bg-current" />
        {{
          authStore.enabled
            ? `${t('settings.user.enabled')}${authStore.username ? ` · ${authStore.username}` : ''}`
            : t('settings.user.disabled')
        }}
      </div>
    </section>

    <!-- 用户名 + 密码 -->
    <section class="flex flex-col gap-2">
      <h4 class="text-sm font-medium text-ink-secondary">{{ t('settings.user.usernameLabel') }}</h4>
      <n-input
        v-model:value="username"
        :placeholder="t('settings.user.usernamePlaceholder')"
        autocomplete="username"
      />
      <h4 class="text-sm font-medium text-ink-secondary">{{ t('settings.user.passwordLabel') }}</h4>
      <n-input
        v-model:value="password"
        type="password"
        show-password-on="click"
        :placeholder="t('settings.user.passwordPlaceholder')"
        autocomplete="new-password"
      />
      <p class="text-xs text-ink-muted">{{ t('settings.user.passwordHint') }}</p>
    </section>

    <div class="flex items-center gap-3">
      <n-button type="primary" :loading="saving" @click="onSaveClick">
        {{ t('settings.user.save') }}
      </n-button>
      <span class="text-xs text-ink-muted">
        {{ authStore.enabled ? t('settings.user.enabledHint') : t('settings.user.disabledHint') }}
      </span>
    </div>
  </div>
</template>
