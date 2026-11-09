import assert from 'node:assert/strict'
import test from 'node:test'
import { createMemoryHistory, createRouter } from 'vue-router'
import { appEntryRoute } from './appEntryRoute.ts'
import { getPlatformSsoConfig } from '../utils/platformSso.ts'

for (const exclusive of [true, false]) {
  test(`root waits for policy and selects the correct entry (exclusive=${exclusive})`, async () => {
    const originalFetch = globalThis.fetch
    globalThis.fetch = async () => new Response(JSON.stringify({ enabled: exclusive, sso_only: exclusive }))
    const router = createRouter({
      history: createMemoryHistory('/weknora/'),
      routes: [appEntryRoute,
        { path: '/sso/platform', component: { render: () => null } },
        { path: '/platform/knowledge-bases', component: { render: () => null } }],
    })
    router.beforeEach(async () => { await getPlatformSsoConfig() })
    try {
      await router.push('/')
      assert.equal(router.currentRoute.value.path, exclusive ? '/sso/platform' : '/platform/knowledge-bases')
    } finally { globalThis.fetch = originalFetch }
  })
}
