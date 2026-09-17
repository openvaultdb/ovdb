// AI agent skills in the web console (capabilities 20 and 21,
// ai-agent-skills#AC:ui-shows-targets-before-install and
// REQ:install-targets-restricted): the consent step shows the purpose, each
// AI agent the server found with its exact directory and the others as not
// found, focuses nothing on Install, writes nothing until Install skill, and
// sends harness ids, never a directory.
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { resetConnection } from '../src/api'
import { currentPath } from '../src/router'
import SkillsScreen from '../src/screens/SkillsScreen.vue'
import { defaultRoutes, installFetch, json } from './fakeServer'

enableAutoUnmount(afterEach)

beforeEach(() => {
  resetConnection()
  vi.stubGlobal('scrollTo', () => {})
})
afterEach(() => {
  vi.unstubAllGlobals()
  window.history.replaceState({}, '', '/')
  currentPath.value = '/'
})

const target = (harness: string, name: string, dir: string, detected: boolean, installed = false) => ({
  harness,
  name,
  skills_dir: `/home/a/.${harness}/skills`,
  dir: `/home/a/.${harness}/skills/${dir}`,
  detected,
  installed,
})

function skill(id: string, dir: string, name: string, purpose: string, example: string, installedForClaude = false) {
  return {
    id,
    dir,
    name,
    purpose,
    example,
    command: `ovdb skills install ${id}`,
    targets: [
      target('claude', 'Claude Code', dir, true, installedForClaude),
      target('codex', 'Codex', dir, false),
    ],
    installed_for: installedForClaude ? ['claude'] : [],
  }
}

const document = (installed = false) => ({
  schema: 1,
  skills: [
    skill('openvaultdb', 'openvaultdb', 'OpenVaultDB skill', 'Teaches your AI agent what OpenVaultDB is.', 'For example: "keep my notes".'),
    skill(
      'todo-demo',
      'openvaultdb-todo-demo',
      'TODO AI skill',
      'Lets your AI agent read and change your To buy and To watch lists.',
      'For example: "add bananas and coffee to my shopping list".',
      installed,
    ),
  ],
  next: [],
})

const installedDocument = {
  schema: 1,
  skill: 'todo-demo',
  dir: 'openvaultdb-todo-demo',
  name: 'TODO AI skill',
  already_up_to_date: false,
  targets: [{ ...document().skills[1].targets[0], result: 'added', installed: true }],
  next: [
    { label: 'Ask your AI agent: "add bananas and coffee to my shopping list"' },
    { label: 'Open TODO app', command: 'ovdb demo open', action: 'open_app' },
    { label: 'Done', action: 'done' },
  ],
}

describe('AI agent skills', () => {
  it('offers the TODO skill after the demo: purpose, exact directory, Codex not found, nothing written', async () => {
    window.history.replaceState({}, '', '/skills?skill=todo-demo&from=/demo')
    let installed = false
    const calls = installFetch({
      ...defaultRoutes,
      'GET /api/local/v1/skills': () => json(200, document(installed)),
      'POST /api/local/v1/skills/install': () => {
        installed = true
        return json(201, installedDocument)
      },
    })
    const wrapper = mount(SkillsScreen, { attachTo: window.document.body })
    await flushPromises()
    const consent = wrapper.get('[data-testid="skill-consent"]')
    expect(consent.get('h1').text()).toBe('Install the TODO AI skill?')
    expect(consent.text()).toContain('Lets your AI agent read and change your To buy and To watch lists.')
    expect(consent.text()).toContain('"add bananas and coffee to my shopping list"')
    const claude = consent.get('[data-harness="claude"]')
    expect(claude.text()).toContain('Claude Code')
    expect(claude.get('code').text()).toBe('/home/a/.claude/skills/openvaultdb-todo-demo')
    expect((claude.get('input').element as HTMLInputElement).checked).toBe(true)
    const codex = consent.get('[data-harness="codex"]')
    expect(codex.text()).toBe('Codex — not found')
    expect(codex.find('input').exists()).toBe(false)
    expect(window.document.activeElement).not.toBe(consent.get('[data-testid="install-skill"]').element)
    expect(calls.some((c) => c.method === 'POST')).toBe(false)

    await consent.get('[data-testid="install-skill"]').trigger('click')
    await flushPromises()
    expect(calls.filter((c) => c.method === 'POST')).toEqual([
      { method: 'POST', path: '/api/local/v1/skills/install', body: { skill: 'todo-demo', harnesses: ['claude'] } },
    ])
    const result = wrapper.get('[data-testid="skill-result"]')
    expect(result.text()).toContain('Installed the TODO AI skill')
    expect(result.text()).toContain('Claude Code: /home/a/.claude/skills/openvaultdb-todo-demo')
    expect(result.text()).toContain('Ask your AI agent: "add bananas and coffee to my shopping list"')
    expect(result.findAll('a').map((a) => [a.text(), a.attributes('href')])).toEqual([
      ['Open TODO app', '/apps/todo/'],
      ['Done', '/'],
    ])
    expect(wrapper.get('[data-skill="todo-demo"] [data-testid="installed-for"]').text()).toBe('Installed for Claude Code')
  })

  it('Not now writes nothing and returns to where the offer came from', async () => {
    window.history.replaceState({}, '', '/skills?skill=todo-demo&from=/demo')
    const calls = installFetch({ ...defaultRoutes, 'GET /api/local/v1/skills': () => json(200, document()) })
    const wrapper = mount(SkillsScreen, { attachTo: window.document.body })
    await flushPromises()
    await wrapper.get('[data-testid="not-now"]').trigger('click')
    await flushPromises()
    expect(currentPath.value).toBe('/demo')
    expect(calls.some((c) => c.method === 'POST')).toBe(false)
  })

  it('an unticked agent is not sent, and nothing is sent with no agent chosen', async () => {
    window.history.replaceState({}, '', '/skills')
    const calls = installFetch({ ...defaultRoutes, 'GET /api/local/v1/skills': () => json(200, document()) })
    const wrapper = mount(SkillsScreen, { attachTo: window.document.body })
    await flushPromises()
    expect(wrapper.get('h1').text()).toBe('AI agent skills')
    expect(wrapper.findAll('[data-skill] h2').map((h) => h.text())).toEqual(['OpenVaultDB skill', 'TODO AI skill'])
    await wrapper.get('[data-testid="offer-openvaultdb"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-harness="claude"] input').setValue(false)
    await wrapper.get('[data-testid="install-skill"]').trigger('click')
    await flushPromises()
    expect(calls.some((c) => c.method === 'POST')).toBe(false)
    await wrapper.get('[data-testid="not-now"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-testid="skill-consent"]').exists()).toBe(false)
  })

  it('shows a refusal with what to do', async () => {
    window.history.replaceState({}, '', '/skills?skill=openvaultdb')
    installFetch({
      ...defaultRoutes,
      'GET /api/local/v1/skills': () => json(200, document()),
      'POST /api/local/v1/skills/install': () =>
        json(400, {
          schema: 1,
          error: {
            code: 'invalid_argument',
            message: "Couldn't install the OpenVaultDB skill",
            reason: "Claude Code wasn't found on this computer.",
            next: [{ label: 'See the skills and where they go', command: 'ovdb skills list' }],
          },
        }),
    })
    const wrapper = mount(SkillsScreen, { attachTo: window.document.body })
    await flushPromises()
    await wrapper.get('[data-testid="install-skill"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toContain("Couldn't install the OpenVaultDB skill")
    expect(wrapper.text()).toContain('ovdb skills list')
  })
})
