import assert from 'node:assert/strict'
import test from 'node:test'
import { userDisplayName, userSecondaryLabel } from './userDisplay'

test('platform labels prefer the email without exposing generated username suffixes', () => {
  const user = { username: 'lff-fff22cde', email: ' lff@test.com ' }
  assert.equal(userDisplayName(user), 'lff@test.com')
  assert.equal(userSecondaryLabel(user), '')
  assert.equal(user.username, 'lff-fff22cde')
  assert.equal(userDisplayName(user, false), 'lff-fff22cde', 'native mode keeps its existing name-first convention')
})

test('missing and synthetic SSO emails fall back to a usable name', () => {
  assert.equal(userDisplayName({ username: 'Platform user', email: 'uuid@platform-sso.invalid' }), 'Platform user')
  assert.equal(userSecondaryLabel({ username: 'Platform user', email: 'UUID@PLATFORM-SSO.INVALID' }), '')
  assert.equal(userDisplayName({ email: 'lff@test.com' }), 'lff@test.com')
  assert.equal(userDisplayName(null), '')
  assert.equal(userDisplayName({ username: ' Existing user ' }), 'Existing user')
})
