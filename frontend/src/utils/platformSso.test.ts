import assert from 'node:assert/strict'
import test from 'node:test'
import { getLoginRoute, getPlatformSsoConfig, platformSsoEntryRedirect, platformSsoMessageTargets, postPlatformSsoMessage, trustedPlatformMessage } from './platformSso.ts'

test('desktop parent is targeted and its token is accepted only with matching source and nonce', () => {
  const calls: unknown[][] = []
  const parent = { postMessage: (...args: unknown[]) => calls.push(args) }
  const origins = ['dsh-app://app', 'https://172.18.127.67']
  const browser = { parent, location: { ancestorOrigins: ['dsh-app://app'] } }
  Object.defineProperty(globalThis, 'window', { value: browser, configurable: true })
  const originalInfo = console.info
  console.info = () => {}
  try {
    assert.equal(postPlatformSsoMessage(origins, 'weknora:sso-ready', 'nonce', { client_id: 'gcs-webnora' }), 1)
    assert.equal(calls.length, 1)
    assert.equal(calls[0][1], '*')
    const event = { source: parent, origin: 'dsh-app://app', data: { nonce: 'nonce' } } as unknown as MessageEvent
    assert.equal(trustedPlatformMessage(event, origins, 'nonce'), true)
    assert.equal(trustedPlatformMessage({ ...event, origin: 'dsh-app://other' } as MessageEvent, origins, 'nonce'), false)
    assert.equal(trustedPlatformMessage(event, origins, 'old-nonce'), false)
    assert.equal(trustedPlatformMessage({ ...event, source: {} } as MessageEvent, origins, 'nonce'), false)
    browser.location.ancestorOrigins = ['https://172.18.127.67']
    assert.deepEqual(platformSsoMessageTargets(origins), ['https://172.18.127.67'])
    browser.location.ancestorOrigins = ['https://attacker.invalid']
    assert.deepEqual(platformSsoMessageTargets(origins), [])
    browser.location.ancestorOrigins = ['null']
    assert.deepEqual(platformSsoMessageTargets(origins), origins)
  } finally { console.info = originalInfo }
})

test('diagnostic ready is posted as a flat object to the parent with a wildcard target', () => {
  const calls: unknown[][] = []
  const logs: unknown[][] = []
  const parent = { postMessage: (...args: unknown[]) => calls.push(args) }
  Object.defineProperty(globalThis, 'window', { value: { parent }, configurable: true })
  const originalInfo = console.info
  console.info = (...args) => { logs.push(args) }
  try {
    postPlatformSsoMessage(['https://172.18.127.67'], 'weknora:sso-ready', 'current-nonce', { client_id: 'gcs-webnora' })
    assert.deepEqual(calls, [[{ type: 'weknora:sso-ready', nonce: 'current-nonce', client_id: 'gcs-webnora' }, '*']])
    assert.equal(logs[0][0], '[SSO] ready-posted')
    assert.equal(JSON.stringify(logs).includes('current-nonce'), false)
  } finally { console.info = originalInfo }
})

test('wildcard ready reaches an unknown parent without trusting its token or broadcasting account messages', () => {
  const calls: unknown[][] = []
  const parent = { postMessage: (...args: unknown[]) => calls.push(args) }
  Object.defineProperty(globalThis, 'window', {
    value: { parent, location: { ancestorOrigins: ['https://unlisted.invalid'] } }, configurable: true,
  })
  const origins = ['dsh-app://app', 'https://172.18.127.67']
  const originalInfo = console.info
  console.info = () => {}
  try {
    assert.equal(postPlatformSsoMessage(origins, 'weknora:sso-ready', 'nonce'), 1)
    assert.equal(calls[0][1], '*')
    assert.equal(postPlatformSsoMessage(origins, 'weknora:sso-authenticated', 'nonce', { user_id: 'user' }), 0)
    assert.equal(calls.length, 1)
    const event = { source: parent, origin: 'https://unlisted.invalid', data: { nonce: 'nonce' } } as unknown as MessageEvent
    assert.equal(trustedPlatformMessage(event, origins, 'nonce'), false)
    assert.equal(trustedPlatformMessage({ ...event, origin: 'null' } as MessageEvent, origins, 'nonce'), false)
  } finally { console.info = originalInfo }
})

test('platform SSO messages require the exact parent window, origin, and current nonce', () => {
  const parent = {}
  Object.defineProperty(globalThis, 'window', { value: { parent }, configurable: true })
  const allowed = ['https://172.18.127.67']
  const valid = { source: parent, origin: allowed[0], data: { nonce: 'one-use' } } as unknown as MessageEvent
  assert.equal(trustedPlatformMessage(valid, allowed, 'one-use'), true)
  assert.equal(trustedPlatformMessage({ ...valid, origin: 'https://attacker.invalid' } as MessageEvent, allowed, 'one-use'), false)
  assert.equal(trustedPlatformMessage({ ...valid, source: {} } as MessageEvent, allowed, 'one-use'), false)
  assert.equal(trustedPlatformMessage(valid, allowed, 'previous-attempt'), false)
})

test('exclusive SSO hides standalone and legacy login pages while preserving the iframe entry', async () => {
  const originalFetch = globalThis.fetch
  const browser: { parent: unknown } = { parent: null }
  browser.parent = browser
  Object.defineProperty(globalThis, 'window', { value: browser, configurable: true })
  Object.defineProperty(globalThis, 'sessionStorage', { value: { getItem: () => null }, configurable: true })
  let enabled = true
  globalThis.fetch = async () => new Response(JSON.stringify({ enabled, sso_only: true }))
  try {
    await getPlatformSsoConfig()
    assert.equal(getLoginRoute(), '/sso/platform')
    assert.equal(getLoginRoute(true), '/sso/platform?logged_out=1')
    assert.equal(platformSsoEntryRedirect('/platform/knowledge-bases'), '/404')
    assert.equal(platformSsoEntryRedirect('/sso/platform'), '/404')
    assert.equal(platformSsoEntryRedirect('/404'), undefined)
    browser.parent = {}
    assert.equal(platformSsoEntryRedirect('/platform/knowledge-bases'), undefined)
    assert.equal(platformSsoEntryRedirect('/sso/platform'), undefined)
    for (const path of ['/login', '/register', '/login/', '/REGISTER/']) {
      assert.equal(platformSsoEntryRedirect(path), '/404')
    }
    assert.equal(platformSsoEntryRedirect('/platform/knowledge-bases', true), '/404')
    enabled = false
    await getPlatformSsoConfig()
    assert.equal(getLoginRoute(), '/login')
    assert.equal(platformSsoEntryRedirect('/login'), undefined)
  } finally {
    globalThis.fetch = originalFetch
  }
})
