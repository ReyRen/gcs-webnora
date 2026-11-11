<template>
  <main class="harness-models">
    <div class="harness-models__workspace">
      <span>{{ $t('tenant.currentTenant') }}</span>
      <t-select v-if="auth.memberships.length > 1" :value="Number(auth.currentTenantId)"
        :options="auth.memberships.map(item => ({ value: item.tenant_id, label: item.tenant_name || String(item.tenant_id) }))"
        :aria-label="$t('tenant.switchTenant')" class="harness-models__workspace-picker" @change="switchWorkspace" />
      <strong v-else>{{ auth.currentTenantName }}</strong>
    </div>
    <t-alert v-if="error" theme="error" :message="$t('modelCatalog.loadFailed')" />
    <template v-if="catalogOpen && auth.isSystemAdmin">
      <t-button variant="text" @click="catalogOpen = false">
        <template #icon><t-icon name="arrow-left" /></template>
        {{ $t('modelSettings.title') }}
      </t-button>
      <ModelCatalog />
    </template>
    <ModelSettings v-else :key="auth.currentTenantId" embedded @models-changed="notifyParent" @open-catalog="catalogOpen = true" />
  </main>
</template>

<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import ModelSettings from './ModelSettings.vue'
import ModelCatalog from '../system/ModelCatalog.vue'
import { useAuthStore } from '@/stores/auth'
import { getPlatformSsoConfig, platformSsoMessageTargets } from '@/utils/platformSso'

const auth = useAuthStore()
const route = useRoute()
const catalogOpen = ref(false)
const error = ref(false)
let origins: string[] = []
let disposed = false
const previousTheme = document.documentElement.getAttribute('theme-mode')
const previousDesignTheme = document.documentElement.getAttribute('tdesign-theme')
if (route.query.theme === 'dark' || route.query.theme === 'light') {
  document.documentElement.setAttribute('theme-mode', route.query.theme)
  document.documentElement.setAttribute('tdesign-theme', route.query.theme)
}

function switchWorkspace(value: unknown) {
  const target = auth.memberships.find(item => item.tenant_id === Number(value))
  if (!target) return
  auth.setSelectedTenant(target.tenant_id, target.tenant_name || String(target.tenant_id))
  catalogOpen.value = false
  notifyParent()
}

function notifyParent() {
  if (window.parent === window || disposed) return
  for (const origin of platformSsoMessageTargets(origins)) {
    // This is an invalidation, not a model or credential payload. The Harness
    // Host re-reads the catalog under its own authenticated backend session.
    window.parent.postMessage({ type: 'weknora:models-updated', tenant_id: auth.currentTenantId }, origin)
  }
}

onMounted(async () => {
  try {
    const config = await getPlatformSsoConfig()
    if (disposed) return
    origins = config.parent_origins || []
    notifyParent()
  } catch { if (!disposed) error.value = true }
})
onUnmounted(() => {
  disposed = true
  for (const [name, value] of [['theme-mode', previousTheme], ['tdesign-theme', previousDesignTheme]] as const) {
    if (value === null) document.documentElement.removeAttribute(name)
    else document.documentElement.setAttribute(name, value)
  }
})
</script>

<style scoped>
.harness-models {
  min-height: 100vh;
  padding: 16px;
  box-sizing: border-box;
  background: var(--td-bg-color-container);
  color: var(--td-text-color-primary);
}
.harness-models__workspace {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 8px;
  margin-bottom: 20px;
  color: var(--td-text-color-secondary);
  font-size: 12px;
}
.harness-models__workspace strong {
  overflow-wrap: anywhere;
  color: var(--td-text-color-primary);
  font-weight: 500;
}
.harness-models__workspace-picker {
  width: min(320px, 100%);
}
</style>
