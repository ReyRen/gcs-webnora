/** The platform embeds this build as its knowledge-management workspace. */
export const KNOWLEDGE_WORKSPACE = import.meta.env?.VITE_KNOWLEDGE_WORKSPACE !== 'false'

const standaloneRoutes = new Set([
  'globalCreatChat', 'kbCreatChat', 'chat', 'agentList',
  'artifactLibrary', 'automationList', 'toolbox',
])

/** Old conversation bookmarks return to knowledge management in this build. */
export function knowledgeWorkspaceRedirect(name: unknown): string | undefined {
  if (typeof name === 'string' && standaloneRoutes.has(name)) {
    return '/platform/knowledge-bases'
  }
}

const workspaceSettings = new Set([
  'general', 'tenant', 'members', 'defaults', 'models', 'ollama',
  'vectorstore', 'parser', 'storage', 'integration-api', 'integration-mcpserver',
  'system-global', 'model-catalog', 'runtime-queues', 'platform-api-keys',
  'system-audit-log', 'system',
])

/** Filters presentation only; existing role and backend checks still apply. */
export function isKnowledgeWorkspaceSetting(section: string): boolean {
  return workspaceSettings.has(section)
}
