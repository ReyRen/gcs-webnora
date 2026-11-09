<!-- Reference component for model/master. This file does not modify that repository. -->
<template>
  <section class="weknora-frame">
    <p v-if="error" role="alert">{{ error }}</p>
    <iframe ref="frame" src="/weknora/sso/platform" title="知识库" referrerpolicy="no-referrer" />
  </section>
</template>

<script setup>
import { onMounted, onUnmounted, ref } from 'vue';
import { getSsoToken } from '@/api/distributed-ai-system';
import { getUserId } from '@/utils/auth';

const CLIENT_ID = 'gcs-webnora'; // Must match the dedicated registered client and WeKnora's backend setting.
const frame = ref(null);
const error = ref('');
const iframeOrigin = window.location.origin; // HTTPS same-origin /weknora/ gateway.
let disposed = false;
let generation = 0;

async function receive(event) {
  if (disposed || event.source !== frame.value?.contentWindow || event.origin !== iframeOrigin) return;
  if (event.data?.type === 'weknora:sso-error') { error.value = event.data.message || '知识库登录失败'; return; }
  if (event.data?.type === 'weknora:sso-authenticated') { error.value = ''; return; }
  if (event.data?.type !== 'weknora:sso-ready' || !/^[a-f0-9]{48}$/.test(event.data.nonce || '')) return;
  if (event.data.client_id !== CLIENT_ID) { error.value = '知识库客户端配置不一致，请联系管理员'; return; }
  const current = ++generation;
  const expectedUserId = String(getUserId() || '');
  const nonce = event.data.nonce;
  // Same-origin request: the browser supplies the main platform's own Session Cookie.
  const [err, response] = await getSsoToken(CLIENT_ID, { headers: { 'Cache-Control': 'no-store' } });
  if (disposed || current !== generation || String(getUserId() || '') !== expectedUserId) return;
  if (err || String(response?.code) !== '0000' || !response?.content?.access_token) {
    error.value = '统一平台登录已失效或领取票据失败，请重新登录平台';
    return;
  }
  frame.value?.contentWindow?.postMessage({
    type: 'weknora:sso-token', nonce,
    access_token: response.content.access_token,
    refresh_token: response.content.refresh_token,
    user_id: expectedUserId || undefined,
  }, iframeOrigin);
}

// Call this from the main platform's existing logout orchestration BEFORE the
// iframe is removed. Also invoke /sso/logout there to invalidate upstream tokens.
function logout() {
  generation += 1;
  frame.value?.contentWindow?.postMessage({ type: 'weknora:sso-logout' }, iframeOrigin);
}
defineExpose({ logout });
onMounted(() => window.addEventListener('message', receive));
onUnmounted(() => { disposed = true; generation += 1; window.removeEventListener('message', receive); });
</script>

<style scoped>
.weknora-frame { height: 100%; min-height: 600px; display: flex; flex-direction: column; }
iframe { width: 100%; flex: 1; min-height: 600px; border: 0; }
</style>
