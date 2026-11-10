import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import * as vue from 'vue'
import { fixture } from '../test-utils/vueFixture'

test('refreshed membership names replace a remembered name without changing the active space or role', t => {
  const values = new Map<string, string>()
  const storage = { getItem: (k: string) => values.get(k) ?? null,
    setItem: (k: string, v: string) => { values.set(k, v) }, removeItem: (k: string) => { values.delete(k) } }
  const f = fixture(t, storage)
  const { useAuthStore } = f.evaluate(readFileSync(new URL('./auth.ts', import.meta.url), 'utf8'), {
    vue, pinia: { defineStore: (_id: string, setup: () => object) => () => vue.reactive(setup()) },
    '@/api/auth': { userInfoFromApi: (u: unknown) => u },
    '@/i18n': { default: { global: { t: (key: string) => key } } },
    '@/composables/useFont': { reloadFontFromStorage() {} }, '@/composables/useTheme': { reloadThemeFromStorage() {} },
    '@/composables/preferenceStorage': { safeRemoveItem() {}, userKey: (key: string) => key, resetMigrationLatch() {} },
    '@/api/agent': { BUILTIN_QUICK_ANSWER_ID: '' },
    '@/stores/chatResources': { useChatResourcesStore: () => ({ invalidate() {} }) },
    '@/stores/editorResources': { useEditorResourcesStore: () => ({ invalidate() {} }) },
    '@/stores/organization': { useOrganizationStore: () => ({ clearState() {} }) },
    '@/stores/sequencedRefresh': { createSequencedRefresh: () => ({ refresh: async () => false, share: async () => false }) },
    '@/utils/authRefresh': { invalidateAuthRefresh() {} },
  })
  const auth = useAuthStore()
  auth.setSelectedTenant(9, "renyuan-fff22cde's Workspace")
  auth.setMemberships([{ tenant_id: 9, tenant_name: 'renyuan@test.com', role: 'viewer' }])
  assert.equal(auth.selectedTenantName, 'renyuan@test.com')
  assert.equal(auth.currentTenantName, 'renyuan@test.com')
  assert.equal(auth.effectiveTenantId, 9)
  assert.equal(auth.currentTenantRole, 'viewer')
  assert.equal(values.get('weknora_selected_tenant_name'), 'renyuan@test.com')
  auth.setMemberships([{ tenant_id: 10, tenant_name: '另一个空间', role: 'owner' }])
  assert.equal(auth.selectedTenantId, 9, 'a missing membership does not switch the active tenant')
  assert.equal(auth.currentTenantRole, '', 'no authority is inferred from the cached name')
})
