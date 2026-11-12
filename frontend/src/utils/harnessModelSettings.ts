import { getPlatformSsoConfig, platformSsoMessageTargets, trustedPlatformMessage } from './platformSso'
import { emitAssistantDataChange } from './assistantDataChange'

/** Open the embedding Harness's native model settings without model or credential payloads. */
export async function openHarnessModelSettings(): Promise<void> {
  const config = await getPlatformSsoConfig()
  const targets = platformSsoMessageTargets(config.parent_origins || [])
  if (window.parent === window || !config.enabled || targets.length === 0) {
    throw new Error('Harness model settings are unavailable')
  }
  for (const origin of targets) {
    window.parent.postMessage({ type: 'weknora:open-model-settings' }, origin)
  }
}

/** Refresh the application's model cache after a trusted Harness catalog change. */
export function installHarnessModelUpdates(): () => void {
  let stopped = false
  let origins: string[] = []
  void getPlatformSsoConfig().then(config => {
    if (!stopped && config.enabled) origins = config.parent_origins || []
  }).catch(() => { /* Model selectors still expose their own load errors. */ })
  const receive = (event: MessageEvent) => {
    if (trustedPlatformMessage(event, origins) && event.data.type === 'weknora:models-updated') {
      emitAssistantDataChange({ area: 'model', operation: 'model.updated' })
    }
  }
  window.addEventListener('message', receive)
  return () => { stopped = true; window.removeEventListener('message', receive) }
}
