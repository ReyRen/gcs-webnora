<template>
  <header class="knowledge-workspace-header">
    <nav class="workspace-nav" :aria-label="t('menu.knowledgeBase')">
      <RouterLink to="/platform/knowledge-bases" class="workspace-nav-link"
        :class="{ 'is-active': !sharedSpaceActive }">
        <t-icon name="book" size="17px" />
        <span>{{ t('menu.knowledgeBase') }}</span>
      </RouterLink>
      <RouterLink v-if="showSharedSpaces" to="/platform/organizations" class="workspace-nav-link"
        :class="{ 'is-active': sharedSpaceActive }">
        <t-icon name="usergroup" size="17px" />
        <span>{{ t('menu.organizations') }}</span>
      </RouterLink>
    </nav>

    <div class="workspace-actions">
      <TenantSelector v-if="authStore.canAccessAllTenants && !authStore.isLiteMode" />
      <t-popup v-else-if="!authStore.isLiteMode" v-model:visible="workspaceMenuVisible" trigger="click"
        placement="bottom-right" :show-arrow="false" @visible-change="refreshMemberships">
        <button type="button" class="workspace-button workspace-picker"
          :aria-label="t('tenant.switcher.menuLabel')" :aria-expanded="workspaceMenuVisible"
          :disabled="switching">
          <t-icon name="system-sum" size="16px" />
          <span>{{ activeWorkspaceName }}</span>
          <t-icon name="chevron-down" size="14px" />
        </button>
        <template #content>
          <div class="workspace-menu" :aria-label="t('tenant.switcher.menuLabel')">
            <div class="workspace-menu-title">{{ t('tenant.switcher.menuLabel') }}</div>
            <button v-for="membership in authStore.memberships" :key="membership.tenant_id" type="button"
              class="workspace-menu-item" :class="{ 'is-current': isCurrent(membership.tenant_id) }"
              :disabled="switching" @click="selectWorkspace(membership)">
              <span class="workspace-menu-info">
                <span>{{ workspaceName(membership.tenant_id, membership.tenant_name) }}</span>
                <small>{{ formatRole(membership.role) }}</small>
              </span>
              <t-icon v-if="isCurrent(membership.tenant_id)" name="check" size="16px" />
            </button>
            <button v-if="authStore.canCreateTenant" type="button" class="workspace-menu-item workspace-create"
              @click="workspaceMenuVisible = false; createWorkspaceVisible = true">
              <t-icon name="add" size="16px" />
              <span>{{ t('tenant.create.action') }}</span>
            </button>
          </div>
        </template>
      </t-popup>

      <GlobalInvitationBell v-if="!authStore.isLiteMode" placement="bottom-right" v-slot="{ count }">
        <button type="button" class="workspace-button workspace-icon-button" :aria-label="t('notifications.title')">
          <t-badge :count="count" :max-count="99" size="small">
            <t-icon name="notification" size="17px" />
          </t-badge>
        </button>
      </GlobalInvitationBell>
      <button type="button" class="workspace-button" @click="openSettings">
        <t-icon name="setting" size="17px" />
        <span>{{ t('general.settings') }}</span>
      </button>
    </div>
    <CreateTenantDialog v-model:visible="createWorkspaceVisible" @created="onWorkspaceCreated" />
  </header>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useAuthStore } from '@/stores/auth'
import { useUIStore } from '@/stores/ui'
import { useDeploymentCapabilitiesStore } from '@/stores/deploymentCapabilities'
import { useHomeTenant, useRoleLabel } from '@/composables/useRoleLabel'
import { navigateAfterTenantSwitch, persistLastActiveTenantPreference, stashTenantSwitchToast } from '@/utils/tenantSwitch'
import CreateTenantDialog from '@/components/CreateTenantDialog.vue'
import GlobalInvitationBell from '@/components/GlobalInvitationBell.vue'
import TenantSelector from '@/components/TenantSelector.vue'

const { t } = useI18n()
const authStore = useAuthStore()
const uiStore = useUIStore()
const capabilities = useDeploymentCapabilitiesStore()
const { isHomeTenant } = useHomeTenant()
const { formatRole } = useRoleLabel()
const route = useRoute()
const router = useRouter()
const workspaceMenuVisible = ref(false)
const createWorkspaceVisible = ref(false)
const switching = ref(false)
let lastMembershipRefresh = 0

const sharedSpaceActive = computed(() => route.name === 'organizationList')
const showSharedSpaces = computed(() => !authStore.isLiteMode && capabilities.isSupported('organizations'))
const isCurrent = (id: number) => Number(authStore.effectiveTenantId) === Number(id)
const workspaceName = (id: number, name?: string | null) =>
  isHomeTenant(id) ? t('tenant.switcher.homeTooltip') : name || `#${id}`
const activeWorkspaceName = computed(() => workspaceName(
  Number(authStore.effectiveTenantId), authStore.selectedTenantName || authStore.tenant?.name,
))

function refreshMemberships(visible: boolean) {
  if (visible && Date.now() - lastMembershipRefresh > 2000) {
    lastMembershipRefresh = Date.now()
    void authStore.refreshFromAuthMe()
  }
}

async function activateWorkspace(id: number, name: string, role?: string) {
  if (switching.value || isCurrent(id)) { workspaceMenuVisible.value = false; return }
  switching.value = true
  workspaceMenuVisible.value = false
  authStore.setSelectedTenant(id, name)
  stashTenantSwitchToast({ name, role: role ? formatRole(role) : undefined, roleEnum: role })
  await Promise.race([
    persistLastActiveTenantPreference(isHomeTenant(id) ? null : id),
    new Promise(resolve => setTimeout(resolve, 400)),
  ])
  navigateAfterTenantSwitch()
}

function selectWorkspace(membership: { tenant_id: number; tenant_name?: string; role: string }) {
  return activateWorkspace(membership.tenant_id,
    workspaceName(membership.tenant_id, membership.tenant_name), membership.role)
}

async function onWorkspaceCreated(workspace: { id: number; name: string }) {
  await authStore.refreshFromAuthMe()
  await activateWorkspace(workspace.id, workspace.name, 'owner')
}

function openSettings() {
  uiStore.openSettings()
  void router.push('/platform/settings')
}
</script>

<style scoped lang="less">
.knowledge-workspace-header {
  flex: 0 0 auto;
  min-height: 48px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 0 24px;
  border-bottom: 1px solid var(--td-component-border);
  background: var(--td-bg-color-container);
}
.workspace-nav, .workspace-actions { display: flex; align-items: center; gap: 8px; min-width: 0; }
.workspace-nav-link, .workspace-button {
  display: inline-flex; align-items: center; justify-content: center; gap: 7px;
  min-height: 32px; padding: 0 10px; border-radius: 6px;
  color: var(--td-text-color-secondary); font: inherit; font-size: 13px;
  text-decoration: none; border: 0; background: transparent; cursor: pointer; white-space: nowrap;
  &:hover { background: var(--td-bg-color-container-hover); color: var(--td-text-color-primary); }
  &:focus-visible { outline: 2px solid var(--td-brand-color); outline-offset: 2px; }
  &:disabled { opacity: .6; cursor: wait; }
}
.workspace-nav-link.is-active { color: var(--td-brand-color); background: var(--td-brand-color-light); font-weight: 600; }
.workspace-picker span { max-width: 160px; overflow: hidden; text-overflow: ellipsis; }
.workspace-icon-button { width: 32px; padding: 0; }
.workspace-menu { width: 248px; max-width: calc(100vw - 40px); max-height: min(480px, 75vh); overflow-y: auto; padding: 4px; }
.workspace-menu-title { padding: 7px 10px; color: var(--td-text-color-placeholder); font-size: 12px; }
.workspace-menu-item {
  display: flex; align-items: center; gap: 8px; width: 100%; padding: 10px;
  text-align: left; border: 0; border-radius: 5px; background: transparent;
  color: var(--td-text-color-primary); font: inherit; font-size: 13px; cursor: pointer;
  &:hover, &:focus-visible { background: var(--td-bg-color-container-hover); }
  &.is-current { color: var(--td-brand-color); }
}
.workspace-menu-info { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 4px;
  > span { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  small { font-size: 11px; color: var(--td-text-color-placeholder); }
}
.workspace-create { border-top: 1px solid var(--td-component-border); border-radius: 0; margin-top: 4px; }
:deep(.tenant-selector) { margin: 0; padding: 0; border: 0; }
:deep(.tenant-trigger) { padding: 4px 10px; }
:deep(.tenant-label) { display: none; }
:deep(.tenant-dropdown) { left: auto; right: 0; width: 280px; max-width: calc(100vw - 24px); }
@media (max-width: 640px) {
  .knowledge-workspace-header { padding: 6px 12px; flex-wrap: wrap; gap: 4px; }
  .workspace-actions { margin-left: auto; }
  .workspace-picker span { max-width: 100px; }
}
</style>
