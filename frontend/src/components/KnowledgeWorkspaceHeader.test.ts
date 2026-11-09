import assert from 'node:assert/strict'
import test, { type TestContext } from 'node:test'
import { reactive } from 'vue'
import { fixture } from '../test-utils/vueFixture'

function setup(t: TestContext, current = 7, home = 7) {
  const f = fixture(t)
  const routes: string[] = []
  const selected: unknown[] = []
  const saved: unknown[] = []
  const toasts: unknown[] = []
  let opened = 0
  let refreshed = 0
  let navigated = 0
  const auth = reactive({
    effectiveTenantId: current, selectedTenantName: 'renyuan-account-space',
    tenant: { name: 'renyuan-account-space' }, isLiteMode: false,
    canAccessAllTenants: false, canCreateTenant: true,
    memberships: [{ tenant_id: 7, tenant_name: 'renyuan-account-space', role: 'owner' },
      { tenant_id: 9, tenant_name: '研发知识库', role: 'viewer' }],
    setSelectedTenant: (id: number, name: string) => { selected.push({ id, name }); auth.effectiveTenantId = id },
    refreshFromAuthMe: async () => { refreshed++ },
  })
  const route = reactive({ name: 'knowledgeBaseList' })
  const capabilities = reactive({ organizations: true })
  const { vm } = f.component(new URL('./KnowledgeWorkspaceHeader.vue', import.meta.url), {
    'vue-router': { useRoute: () => route, useRouter: () => ({ push: (path: string) => routes.push(path) }) },
    'vue-i18n': { useI18n: () => ({ t: (key: string) => key === 'tenant.switcher.homeTooltip' ? '我的空间' : key }) },
    '@/stores/auth': { useAuthStore: () => auth },
    '@/stores/ui': { useUIStore: () => ({ openSettings: () => { opened++ } }) },
    '@/stores/deploymentCapabilities': { useDeploymentCapabilitiesStore: () => ({ isSupported: () => capabilities.organizations }) },
    '@/composables/useRoleLabel': { useHomeTenant: () => ({ isHomeTenant: (id: number) => id === home }),
      useRoleLabel: () => ({ formatRole: (role: string) => role }) },
    '@/utils/tenantSwitch': {
      persistLastActiveTenantPreference: async (id: number | null) => { saved.push(id) },
      stashTenantSwitchToast: (value: unknown) => toasts.push(value),
      navigateAfterTenantSwitch: () => { navigated++ },
    },
  })
  return { vm, auth, route, capabilities, routes, selected, saved, toasts,
    opened: () => opened, refreshed: () => refreshed, navigated: () => navigated }
}

test('configuration stays accessible without the account menu', t => {
  const f = setup(t)
  assert.equal(f.vm.activeWorkspaceName, '我的空间', 'personal workspace does not repeat the account name')
  f.vm.openSettings()
  assert.equal(f.opened(), 1)
  assert.deepEqual(f.routes, ['/platform/settings'])
})

test('switching space keeps tenant scoping, role and persisted selection', async t => {
  const f = setup(t)
  await f.vm.selectWorkspace(f.auth.memberships[1])
  assert.deepEqual(f.selected, [{ id: 9, name: '研发知识库' }])
  assert.deepEqual(f.saved, [9])
  assert.equal((f.toasts[0] as any).roleEnum, 'viewer')
  assert.equal(f.navigated(), 1)
  await f.vm.selectWorkspace(f.auth.memberships[0])
  assert.equal(f.navigated(), 1, 'a second click cannot race the pending reload')
})

test('switching home explicitly scopes requests and clears the remembered override', async t => {
  const f = setup(t, 9)
  await f.vm.selectWorkspace(f.auth.memberships[0])
  assert.deepEqual(f.selected, [{ id: 7, name: '我的空间' }])
  assert.deepEqual(f.saved, [null])
  assert.equal(f.navigated(), 1)
})

test('current space is a no-op and new spaces reuse the existing switch flow', async t => {
  const f = setup(t)
  await f.vm.selectWorkspace(f.auth.memberships[0])
  assert.equal(f.selected.length, 0)
  await f.vm.onWorkspaceCreated({ id: 11, name: '项目文档' })
  assert.equal(f.refreshed(), 1)
  assert.deepEqual(f.selected, [{ id: 11, name: '项目文档' }])
  assert.equal((f.toasts[0] as any).roleEnum, 'owner')
})

test('shared-space entry follows deployment capabilities and Lite restrictions', t => {
  const f = setup(t)
  assert.equal(f.vm.showSharedSpaces, true)
  f.capabilities.organizations = false
  assert.equal(f.vm.showSharedSpaces, false)
  f.capabilities.organizations = true
  f.auth.isLiteMode = true
  assert.equal(f.vm.showSharedSpaces, false)
})
