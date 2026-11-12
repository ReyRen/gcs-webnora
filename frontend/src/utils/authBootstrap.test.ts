import assert from 'node:assert/strict'
import test from 'node:test'
import { restoreBootstrapSession } from './authBootstrap.ts'

test('a platform entry waits for its fresh ticket without sending an expired cached credential', async () => {
  for (const basePath of ['', '/', '/weknora', '/app/weknora/']) {
    for (const suffix of ['', '/']) {
      await restoreBootstrapSession({
        pathname: `${basePath.replace(/\/+$/, '')}/sso/platform${suffix}`,
        basePath, hasToken: true,
        restore: async () => { assert.fail('cached authentication redirected before platform SSO') },
      })
    }
  }
})

test('ordinary authenticated pages still restore the saved session and propagate a failed refresh', async () => {
  let restored = 0
  const restore = async () => { restored++ }
  await restoreBootstrapSession({ pathname: '/weknora/platform/knowledge-bases', basePath: '/weknora', hasToken: true, restore })
  assert.equal(restored, 1)
  await restoreBootstrapSession({ pathname: '/weknora/platform/knowledge-bases', basePath: '/weknora', hasToken: false, restore })
  assert.equal(restored, 1)
  await assert.rejects(restoreBootstrapSession({
    pathname: '/weknora/harness/models', basePath: '/weknora', hasToken: true,
    restore: async () => { throw new Error('expired cached session') },
  }), /expired cached session/u)
})
