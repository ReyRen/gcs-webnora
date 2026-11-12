import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { runInNewContext } from 'node:vm'
import test from 'node:test'
import ts from 'typescript'
import { isKnowledgeWorkspaceSetting, knowledgeWorkspaceRedirect } from './knowledgeWorkspace'
import { SETTINGS_SECTION_MIN_ROLE, SYSTEM_ADMIN_SETTINGS_SECTIONS as SYSTEM_ADMIN_SECTIONS } from './settingsAccess'
import { integrationTabFromSection, isIntegrationSection } from './settingsRoute'
import { INTEGRATION_TAB_MIN_ROLE } from './integrations'

test('conversation and agent bookmarks return to knowledge management', () => {
  for (const name of ['globalCreatChat', 'kbCreatChat', 'chat', 'agentList', 'artifactLibrary', 'automationList', 'toolbox']) {
    assert.equal(knowledgeWorkspaceRedirect(name), '/platform/knowledge-bases')
  }
  for (const name of ['knowledgeBaseList', 'knowledgeBaseDetail', 'knowledgeBaseSettings', 'organizationList', 'settings', undefined]) {
    assert.equal(knowledgeWorkspaceRedirect(name), undefined)
  }
})

test('knowledge configuration remains available while independent-app panels are hidden', () => {
  for (const section of ['general', 'tenant', 'members', 'defaults', 'vectorstore',
    'parser', 'storage', 'integration-api', 'integration-mcpserver', 'system-global', 'platform-api-keys']) {
    assert.equal(isKnowledgeWorkspaceSetting(section), true, section)
  }
  for (const section of ['models', 'ollama', 'weknoracloud', 'model-catalog', 'assistant', 'userprofile', 'chathistory', 'memory', 'mymemory', 'websearch', 'sandbox', 'envvars', 'integration-im']) {
    assert.equal(isKnowledgeWorkspaceSetting(section), false, section)
  }
})

// Exercise the actual Settings visibility decision, including existing role checks.
const source = readFileSync(new URL('../views/settings/Settings.vue', import.meta.url), 'utf8')
const script = source.match(/<script setup lang="ts">([\s\S]*?)<\/script>/)![1]!
const ast = ts.createSourceFile('Settings.ts', script, ts.ScriptTarget.Latest, true)
const visibility = ast.statements.find(statement => ts.isVariableStatement(statement) &&
  statement.declarationList.declarations.some(declaration => ts.isIdentifier(declaration.name) && declaration.name.text === 'canSeeSection'))!
const compiled = ts.transpileModule(visibility.getText(ast), { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText
function visible(section: string, role: string, workspace = true, systemAdmin = false): boolean {
  const roles = ['viewer', 'contributor', 'admin', 'owner']
  return runInNewContext(`${compiled}\ncanSeeSection(section)`, {
    KNOWLEDGE_WORKSPACE: workspace, isKnowledgeWorkspaceSetting, section,
    isIntegrationSection, integrationTabFromSection, INTEGRATION_TAB_MIN_ROLE,
    SETTINGS_SECTION_MIN_ROLE, SYSTEM_ADMIN_SECTIONS,
    authStore: { canAccessAllTenants: false, isSystemAdmin: systemAdmin,
      hasRole: (minimum: string) => roles.indexOf(role) >= roles.indexOf(minimum) },
  })
}

test('the simplified configuration keeps existing tenant and system permissions', () => {
  assert.equal(visible('models', 'viewer'), false, 'Harness owns model administration in the knowledge workspace')
  assert.equal(visible('models', 'viewer', false), true, 'standalone deployments retain model administration')
  assert.equal(visible('parser', 'viewer'), false)
  assert.equal(visible('parser', 'admin'), true)
  assert.equal(visible('system-global', 'owner'), false)
  assert.equal(visible('system-global', 'owner', true, true), true)
  assert.equal(visible('general', 'viewer'), true)
  assert.equal(visible('assistant', 'owner'), false)
  assert.equal(visible('userprofile', 'viewer'), false)
  assert.equal(visible('userprofile', 'viewer', false), true, 'standalone build retains the old account settings')
})
