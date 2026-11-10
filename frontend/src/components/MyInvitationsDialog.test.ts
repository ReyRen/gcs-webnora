import assert from 'node:assert/strict'
import test, { type TestContext } from 'node:test'
import { reactive, ref } from 'vue'
import { fixture, flush } from '../test-utils/vueFixture'
import type { TenantInvitation } from '../api/tenant/invitations'

const invitation: TenantInvitation = {
  id: 11, tenant_id: 9, tenant_name: '研发空间', role: 'viewer', status: 'pending',
  invitee_user_id: 'invitee', inviter_name: 'renyuan-fff22cde', inviter_email: 'renyuan@test.com',
  created_at: '2026-11-10T00:00:00Z', expires_at: '2026-11-17T00:00:00Z',
}

async function setup(t: TestContext) {
  const f = fixture(t)
  const messages = { success: t.mock.fn(), warning: t.mock.fn(), error: t.mock.fn() }
  const accept = t.mock.fn(async (_id: number) => ({ success: true }))
  const auth = reactive({
    memberships: [{ tenant_id: 7, tenant_name: '我的空间', role: 'owner' }],
    pendingInvitationCount: 1,
    setPendingInvitationCount(count: number) { this.pendingInvitationCount = count },
    refreshFromAuthMe: t.mock.fn(async () => {
      auth.memberships.push({ tenant_id: 9, tenant_name: '研发空间', role: 'contributor' })
      return true
    }),
    setSelectedTenant: t.mock.fn(),
  })
  const persist = t.mock.fn(async (_id: number | null) => {})
  const toast = t.mock.fn()
  const navigate = t.mock.fn()
  const { vm } = f.component(new URL('./MyInvitationsDialog.vue', import.meta.url), {
    'vue-i18n': { useI18n: () => ({ t: (key: string) => key, locale: ref('zh-CN') }) },
    'tdesign-vue-next': { MessagePlugin: messages },
    '@/stores/auth': { useAuthStore: () => auth },
    '@/composables/useRoleLabel': { useHomeTenant: () => ({ isHomeTenant: (id: number) => id === 7 }) },
    '@/utils/tenantSwitch': { persistLastActiveTenantPreference: persist, stashTenantSwitchToast: toast,
      navigateAfterTenantSwitch: navigate },
    '@/api/tenant/invitations': { acceptInvitation: accept,
      listMyInvitations: async () => ({ success: true, data: { invitations: [invitation] } }) },
  }, { visible: false })
  await vm.reload()
  await flush()
  return { vm, auth, accept, messages, persist, toast, navigate }
}

test('accept refreshes membership immediately and keeps a direct entry without changing the current space', async t => {
  const f = await setup(t)
  await f.vm.onAccept(invitation)
  assert.equal(f.vm.accepted[11], 'ready')
  assert.equal(f.auth.pendingInvitationCount, 0)
  assert.equal(f.vm.invitations.length, 1, 'accepted row stays available for entering the joined space')
  assert.equal(f.auth.refreshFromAuthMe.mock.callCount(), 1)
  assert.equal(f.navigate.mock.callCount(), 0, 'accepting does not discard the current page')
  assert.equal(f.vm.inviterDisplay(invitation), 'renyuan@test.com')
  await f.vm.enterWorkspace(invitation)
  assert.equal(f.auth.setSelectedTenant.mock.calls[0].arguments[0], 9)
  assert.equal(f.auth.setSelectedTenant.mock.calls[0].arguments[1], '研发空间')
  assert.equal(f.persist.mock.calls[0].arguments[0], 9)
  assert.equal((f.toast.mock.calls[0].arguments[0] as any).roleEnum, 'contributor', 'use refreshed role, not stale invitation role')
  assert.equal(f.navigate.mock.callCount(), 1)
  await f.vm.enterWorkspace(invitation)
  assert.equal(f.navigate.mock.callCount(), 1, 'serialize clicks while navigation is pending')
})

test('a failed post-accept refresh can be retried without accepting twice or decrementing the badge again', async t => {
  const f = await setup(t)
  f.auth.refreshFromAuthMe.mock.mockImplementationOnce(async () => false)
  await f.vm.onAccept(invitation)
  assert.equal(f.vm.accepted[11], 'failed')
  assert.equal(f.messages.success.mock.callCount(), 0)
  assert.equal(f.messages.warning.mock.callCount(), 1)
  await f.vm.enterWorkspace(invitation)
  assert.equal(f.navigate.mock.callCount(), 0)
  await f.vm.onAccept(invitation)
  await f.vm.retryAcceptedInvitation(invitation)
  assert.equal(f.vm.accepted[11], 'ready')
  assert.equal(f.accept.mock.callCount(), 1)
  assert.equal(f.auth.pendingInvitationCount, 0)
  assert.equal(f.auth.refreshFromAuthMe.mock.callCount(), 2)
})

test('missing membership and rejected invitations never allow entry or report success', async t => {
  const f = await setup(t)
  f.auth.refreshFromAuthMe.mock.mockImplementationOnce(async () => true)
  await f.vm.onAccept(invitation)
  assert.equal(f.vm.accepted[11], 'failed')
  await f.vm.enterWorkspace(invitation)
  assert.equal(f.navigate.mock.callCount(), 0)
  const other = { ...invitation, id: 12 }
  f.accept.mock.mockImplementationOnce(async () => { throw { status: 403 } })
  await f.vm.onAccept(other)
  assert.equal(f.vm.accepted[12], undefined)
  assert.equal(f.messages.error.mock.callCount(), 1)
  assert.equal(f.auth.refreshFromAuthMe.mock.callCount(), 1)
  assert.equal(f.messages.success.mock.callCount(), 0)
})
