<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import NotFound from '@/views/NotFound.vue'
import { getApiBaseUrl } from '@/utils/api-base'
import { getPlatformSsoConfig, markPlatformSsoFrame, postPlatformSsoMessage, trustedPlatformMessage } from '@/utils/platformSso'

const router = useRouter()
const route = useRoute()
const auth = useAuthStore()
const status = ref('加载中…')
const failed = ref(false)
const standalone = window.parent === window
let origins: string[] = []
let nonce = ''
let consumed = false
let stopped = false
let timer: ReturnType<typeof setTimeout> | undefined

function post(type: string, extra: Record<string, unknown> = {}) {
  return postPlatformSsoMessage(origins, type, nonce, extra)
}

function fail(message: string) {
  consumed = true
  failed.value = true
  status.value = '暂时无法打开页面，请稍后重试'
  console.warn('[SSO] handshake-failed', { reason: message })
  post('weknora:sso-error', { message })
}

async function receive(event: MessageEvent) {
  const type = event.data?.type
  if (type !== 'weknora:sso-token') {
    if (event.source === window.parent && (type === 'sso-token' || type === 'INIT_DATA')) {
      console.warn('[SSO] unexpected-message-type', { receivedType: type, expectedType: 'weknora:sso-token' })
    }
    return
  }
  if (stopped || consumed || !trustedPlatformMessage(event, origins, nonce)) {
    console.warn('[SSO] token-message-rejected', {
      origin: event.origin, parentMatched: event.source === window.parent,
      originMatched: origins.includes(event.origin), nonceMatched: event.data.nonce === nonce, stopped, consumed,
    })
    return
  }
  consumed = true
  clearTimeout(timer)
  const token = event.data.access_token
  if (typeof token !== 'string' || !token || token.length > 32768) { fail('未收到有效的平台登录票据'); return }
  console.info('[SSO] token-message-accepted')
  status.value = '加载中…'
  try {
    const response = await fetch(`${getApiBaseUrl()}/api/v1/auth/platform-sso/exchange`, {
      method: 'POST', cache: 'no-store', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        access_token: token,
        refresh_token: typeof event.data.refresh_token === 'string' ? event.data.refresh_token : '',
        expected_user_id: event.data.user_id == null ? '' : String(event.data.user_id),
      }),
    })
    const result = await response.json()
    console.info('[SSO] exchange-result', { status: response.status, success: result.success === true })
    if (stopped) return
    if (!response.ok || !result.success || !result.token) throw new Error(result.message || '平台身份验证失败')
    auth.setToken(result.token)
    if (result.refresh_token) auth.setRefreshToken(result.refresh_token)
    // Reuse the existing /auth/me hydration for permissions, active workspace and capabilities.
    const hydrated = await auth.refreshFromAuthMe()
    if (stopped) { auth.logout(); return }
    if (!hydrated) throw new Error('无法加载账号和空间信息')
    post('weknora:sso-authenticated', { user_id: result.user.id })
    await router.replace(auth.hasValidTenant ? '/platform/knowledge-bases' : '/onboarding/workspace')
  } catch (error) {
    auth.logout()
    fail(error instanceof Error ? error.message : '平台身份验证失败')
  }
}

async function start() {
  failed.value = false
  consumed = false
  status.value = '加载中…'
  clearTimeout(timer)
  let stage = 'load-config'
  try {
    const config = await getPlatformSsoConfig()
    if (stopped) return
    if (!config.enabled) { fail('平台单点登录尚未配置，请联系管理员'); return }
    origins = config.parent_origins || []
    if (!origins.length) { fail('未配置统一平台来源'); return }
    console.info('[SSO] config-ready', { origin: window.location.origin, targets: origins, framed: !standalone })
    stage = 'clear-previous-session'
    auth.logout() // Never reuse a previous platform user's browser credentials.
    stage = 'mark-iframe'
    markPlatformSsoFrame()
    stage = 'create-nonce'
    const bytes = crypto.getRandomValues(new Uint8Array(24))
    nonce = Array.from(bytes, b => b.toString(16).padStart(2, '0')).join('')
    timer = setTimeout(() => fail('未收到统一平台登录响应，请重新进入或重试'), 20000)
    stage = 'post-ready'
    if (post('weknora:sso-ready', { client_id: config.client_id }) === 0) {
      clearTimeout(timer)
      fail('父页面来源不在允许列表中')
    }
  } catch (error) {
    console.error('[SSO] startup-failed', { stage, errorName: error instanceof Error ? error.name : 'unknown' })
    fail('无法连接认证服务，请稍后重试')
  }
}

onMounted(() => {
  console.info('[SSO] entry-mounted', { origin: window.location.origin, framed: !standalone })
  if (standalone) return
  window.addEventListener('message', receive)
  window.addEventListener('weknora:platform-logout', cancelLogin)
  if (route.query.logged_out === '1') {
    status.value = '已退出'
    return
  }
  void start()
})
function cancelLogin() { stopped = true; consumed = true; clearTimeout(timer) }
onUnmounted(() => {
  cancelLogin()
  window.removeEventListener('message', receive)
  window.removeEventListener('weknora:platform-logout', cancelLogin)
})
</script>

<template>
  <NotFound v-if="standalone" />
  <main v-else class="platform-sso" aria-live="polite">
    <p>{{ status }}</p>
    <button v-if="failed" type="button" @click="start">重试</button>
  </main>
</template>

<style scoped>
.platform-sso { min-height: 100vh; display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 16px; color: #334155; background: #f8fafc; }
button { padding: 8px 24px; cursor: pointer; border: 1px solid #cbd5e1; border-radius: 6px; background: white; }
</style>
