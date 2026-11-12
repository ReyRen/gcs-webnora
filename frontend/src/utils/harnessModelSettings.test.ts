import assert from 'node:assert/strict'
import test from 'node:test'
import { openHarnessModelSettings, installHarnessModelUpdates } from './harnessModelSettings.ts'
import { ASSISTANT_DATA_CHANGE_EVENT } from './assistantDataChange.ts'

test('only the configured Harness parent can open settings and invalidate model choices', async t => {
  const events = new EventTarget()
  const sent: unknown[][] = []
  const changes: unknown[] = []
  const parent = { postMessage: (...args: unknown[]) => { sent.push(args) } }
  const browser = {
    parent, location: { ancestorOrigins: ['https://harness.test'] },
    addEventListener: events.addEventListener.bind(events),
    removeEventListener: events.removeEventListener.bind(events),
    dispatchEvent: events.dispatchEvent.bind(events),
  }
  const oldWindow = Object.getOwnPropertyDescriptor(globalThis, 'window')
  const oldFetch = globalThis.fetch
  Object.defineProperty(globalThis, 'window', { configurable: true, value: browser })
  globalThis.fetch = async () => Response.json({ enabled: true, parent_origins: ['https://harness.test'] })
  t.after(() => {
    globalThis.fetch = oldFetch
    if (oldWindow) Object.defineProperty(globalThis, 'window', oldWindow)
    else Reflect.deleteProperty(globalThis, 'window')
  })
  events.addEventListener(ASSISTANT_DATA_CHANGE_EVENT, event => changes.push((event as CustomEvent).detail))
  const stop = installHarnessModelUpdates()
  await openHarnessModelSettings()
  assert.deepEqual(sent, [[{ type: 'weknora:open-model-settings' }, 'https://harness.test']])
  const receive = (origin: string, source: unknown = parent) => {
    const event = new Event('message')
    Object.assign(event, { origin, source, data: { type: 'weknora:models-updated' } })
    events.dispatchEvent(event)
  }
  receive('https://evil.test')
  receive('https://harness.test', {})
  assert.equal(changes.length, 0)
  receive('https://harness.test')
  assert.equal(changes.length, 1)
  stop()
  receive('https://harness.test')
  assert.equal(changes.length, 1)
  browser.location.ancestorOrigins = ['https://evil.test']
  await assert.rejects(openHarnessModelSettings(), /unavailable/)
})
