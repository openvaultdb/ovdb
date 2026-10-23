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
  state: installed ? 'installed' : 'not_installed',
  state_reason: undefined as string | undefined,
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
      'For example: "add tea to my shopping list and Arrival to my watch list".',
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
  targets: [{ ...document().skills[1].targets[0], result: 'added', installed: true, state: 'installed' }],
  next: [
    { label: 'Ask your AI agent: "add tea to my shopping list and Arrival to my watch list"' },
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
      'GET /api/local/v1/skills?adoptable=1&recovery=1': () => json(200, document(installed)),
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
    expect(consent.text()).toContain('"add tea to my shopping list and Arrival to my watch list"')
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
      { method: 'POST', path: '/api/local/v1/skills/install', body: { skill: 'todo-demo', harnesses: ['claude'], replace_changed: false } },
    ])
    const result = wrapper.get('[data-testid="skill-result"]')
    expect(result.text()).toContain('Installed the TODO AI skill')
    expect(result.text()).toContain('Claude Code: /home/a/.claude/skills/openvaultdb-todo-demo')
    expect(result.text()).toContain('Ask your AI agent: "add tea to my shopping list and Arrival to my watch list"')
    expect(result.findAll('a').map((a) => [a.text(), a.attributes('href')])).toEqual([
      ['Open TODO app', '/apps/todo/'],
      ['Done', '/'],
    ])
    expect(wrapper.get('[data-skill="todo-demo"] [data-testid="installed-for"]').text()).toBe('Installed for Claude Code')
  })

  it('Not now writes nothing and returns to where the offer came from', async () => {
    window.history.replaceState({}, '', '/skills?skill=todo-demo&from=/demo')
    const calls = installFetch({ ...defaultRoutes, 'GET /api/local/v1/skills?adoptable=1&recovery=1': () => json(200, document()) })
    const wrapper = mount(SkillsScreen, { attachTo: window.document.body })
    await flushPromises()
    await wrapper.get('[data-testid="not-now"]').trigger('click')
    await flushPromises()
    expect(currentPath.value).toBe('/demo')
    expect(calls.some((c) => c.method === 'POST')).toBe(false)
  })

  it('an unticked agent is not sent, and nothing is sent with no agent chosen', async () => {
    window.history.replaceState({}, '', '/skills')
    const calls = installFetch({ ...defaultRoutes, 'GET /api/local/v1/skills?adoptable=1&recovery=1': () => json(200, document()) })
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

  it('offers a changed copy unticked, and replaces it only when ticked (review F7)', async () => {
    window.history.replaceState({}, '', '/skills?skill=todo-demo')
    const changed = document()
    changed.skills[1].targets[0] = { ...changed.skills[1].targets[0], installed: true, state: 'changed' }
    changed.skills[1].targets[1] = { ...changed.skills[1].targets[1], detected: true, state: 'not_ovdb' }
    const calls = installFetch({
      ...defaultRoutes,
      'GET /api/local/v1/skills?adoptable=1&recovery=1': () => json(200, changed),
      'POST /api/local/v1/skills/install': () => json(200, installedDocument),
    })
    const wrapper = mount(SkillsScreen, { attachTo: window.document.body })
    await flushPromises()
    const claude = wrapper.get('[data-harness="claude"]')
    expect((claude.get('input').element as HTMLInputElement).checked).toBe(false)
    expect(claude.text()).toContain('changed since install — installing replaces your changes')
    expect(wrapper.get('[data-harness="codex"]').text()).toBe('Codex — another skill with this name')
    await wrapper.get('[data-testid="install-skill"]').trigger('click')
    await flushPromises()
    expect(calls.some((c) => c.method === 'POST')).toBe(false)
    await claude.get('input').setValue(true)
    await wrapper.get('[data-testid="install-skill"]').trigger('click')
    await flushPromises()
    expect(calls.find((c) => c.method === 'POST')?.body).toEqual({ skill: 'todo-demo', harnesses: ['claude'], replace_changed: true })
  })

  it('offers a copy that was already there unticked, and adopts it only when ticked, saying where the copy is kept', async () => {
    window.history.replaceState({}, '', '/skills?skill=todo-demo')
    const adoptable = document()
    adoptable.skills[1].targets[0] = { ...adoptable.skills[1].targets[0], state: 'adoptable' }
    let adopted = false
    const calls = installFetch({
      ...defaultRoutes,
      'GET /api/local/v1/skills?adoptable=1&recovery=1': () => json(200, adoptable),
      'POST /api/local/v1/skills/install': () => {
        adopted = true
        return json(201, {
          ...installedDocument,
          targets: [
            {
              ...installedDocument.targets[0],
              result: 'adopted',
              backup_path: '/home/a/.claude/skills/.cli-helpers-skills-adopted-backup/20261004T120000.000000000Z/openvaultdb-todo-demo',
            },
          ],
        })
      },
    })
    const wrapper = mount(SkillsScreen, { attachTo: window.document.body })
    await flushPromises()
    expect(adopted).toBe(false)
    const claude = wrapper.get('[data-harness="claude"]')
    expect((claude.get('input').element as HTMLInputElement).checked).toBe(false)
    expect(claude.text()).toContain('already here — installing takes it over and keeps a backup of your copy')
    await wrapper.get('[data-testid="install-skill"]').trigger('click')
    await flushPromises()
    expect(calls.some((c) => c.method === 'POST')).toBe(false)
    await claude.get('input').setValue(true)
    await wrapper.get('[data-testid="install-skill"]').trigger('click')
    await flushPromises()
    expect(calls.find((c) => c.method === 'POST')?.body).toEqual({ skill: 'todo-demo', harnesses: ['claude'], replace_changed: false, adopt_harnesses: ['claude'] })
    expect(wrapper.get('[data-testid="skill-result"]').text()).toContain(
      'Claude Code: /home/a/.claude/skills/openvaultdb-todo-demo (already there, now managed by OVDB; your copy is kept at ' +
        '/home/a/.claude/skills/.cli-helpers-skills-adopted-backup/20261004T120000.000000000Z/openvaultdb-todo-demo)',
    )
  })

  it('sends only the agents it showed as having a copy to take over, never one that is merely ticked', async () => {
    window.history.replaceState({}, '', '/skills?skill=todo-demo')
    const both = document()
    both.skills[1].targets = [
      { ...both.skills[1].targets[0], state: 'adoptable' },
      { ...both.skills[1].targets[0], harness: 'codex', name: 'Codex', detected: true, dir: '/home/a/.codex/skills/openvaultdb-todo-demo', skills_dir: '/home/a/.codex/skills', state: 'not_installed' },
    ]
    const calls = installFetch({
      ...defaultRoutes,
      'GET /api/local/v1/skills?adoptable=1&recovery=1': () => json(200, both),
      'POST /api/local/v1/skills/install': () => json(201, installedDocument),
    })
    const wrapper = mount(SkillsScreen, { attachTo: window.document.body })
    await flushPromises()
    await wrapper.get('[data-harness="claude"] input').setValue(true)
    await wrapper.get('[data-testid="install-skill"]').trigger('click')
    await flushPromises()
    expect(calls.find((c) => c.method === 'POST')?.body).toEqual({
      skill: 'todo-demo',
      harnesses: ['codex', 'claude'],
      replace_changed: false,
      adopt_harnesses: ['claude'],
    })
  })

  it('says an interrupted install is one, with a note, on the list and the consent step', async () => {
    window.history.replaceState({}, '', '/skills')
    const pending = document()
    pending.skills[1].targets[0] = { ...pending.skills[1].targets[0], state: 'recovery_pending', state_reason: 'skills sync recovery is pending' }
    installFetch({ ...defaultRoutes, 'GET /api/local/v1/skills?adoptable=1&recovery=1': () => json(200, pending) })
    const list = mount(SkillsScreen, { attachTo: window.document.body })
    await flushPromises()
    expect(list.get('[data-skill="todo-demo"] [data-testid="interrupted-for"]').text()).toBe('Interrupted install, installing finishes it or says what to do: Claude Code')
    list.unmount()
    window.history.replaceState({}, '', '/skills?skill=todo-demo')
    const wrapper = mount(SkillsScreen, { attachTo: window.document.body })
    await flushPromises()
    const claude = wrapper.get('[data-harness="claude"]')
    expect(claude.text()).toContain('an earlier install here was interrupted — installing finishes it, or says what to do')
    expect((claude.get('input').element as HTMLInputElement).checked).toBe(true)
  })

  it("shows the library's reason for a folder that is another skill, such as a stray file in a copy", async () => {
    window.history.replaceState({}, '', '/skills?skill=todo-demo')
    const stray = document()
    stray.skills[1].targets[0] = {
      ...stray.skills[1].targets[0],
      state: 'not_ovdb',
      state_reason: 'unmanaged target: openvaultdb-todo-demo/.DS_Store is not part of this skill\'s bundle; remove it or back up the folder yourself before syncing',
    }
    installFetch({ ...defaultRoutes, 'GET /api/local/v1/skills?adoptable=1&recovery=1': () => json(200, stray) })
    const wrapper = mount(SkillsScreen, { attachTo: window.document.body })
    await flushPromises()
    expect(wrapper.get('[data-harness="claude"]').text()).toContain('another skill with this name')
    expect(wrapper.get('[data-harness="claude"]').text()).toContain('.DS_Store is not part of this skill')
  })

  it("says OVDB's own skill whose record cannot be read is that, with the library's reason, and does not offer it", async () => {
    window.history.replaceState({}, '', '/skills?skill=todo-demo')
    const broken = document()
    broken.skills[1].targets[0] = { ...broken.skills[1].targets[0], state: 'record_unusable', state_reason: 'skills sync state is corrupt: parse x: unexpected end of JSON input' }
    installFetch({ ...defaultRoutes, 'GET /api/local/v1/skills?adoptable=1&recovery=1': () => json(200, broken) })
    const wrapper = mount(SkillsScreen, { attachTo: window.document.body })
    await flushPromises()
    const claude = wrapper.get('[data-harness="claude"]')
    expect(claude.text()).toContain("its record can't be read")
    expect(claude.text()).toContain('unexpected end of JSON input')
    expect(claude.find('input').exists()).toBe(false)
  })

  it('shows a state this build has no text for as it came instead of blanking the screen', async () => {
    window.history.replaceState({}, '', '/skills')
    const newer = document(true)
    newer.skills[1].targets[0] = { ...newer.skills[1].targets[0], installed: true, state: 'from_a_newer_ovdb' as never }
    installFetch({ ...defaultRoutes, 'GET /api/local/v1/skills?adoptable=1&recovery=1': () => json(200, newer) })
    const wrapper = mount(SkillsScreen, { attachTo: window.document.body })
    await flushPromises()
    expect(wrapper.get('[data-skill="todo-demo"] [data-testid="installed-for"]').text()).toBe('Installed for Claude Code (from_a_newer_ovdb)')
  })

  it('a failed install says which agents it did change, and the list is read again', async () => {
    window.history.replaceState({}, '', '/skills?skill=openvaultdb')
    let reads = 0
    installFetch({
      ...defaultRoutes,
      'GET /api/local/v1/skills?adoptable=1&recovery=1': () => {
        reads++
        return json(200, document())
      },
      'POST /api/local/v1/skills/install': () =>
        json(409, {
          schema: 1,
          error: {
            code: 'already_exists',
            message: "Couldn't install the OpenVaultDB skill",
            reason:
              '/home/a/.codex/skills/openvaultdb already exists and wasn\'t installed by OVDB, so it was left as it is. ' +
              'Before it stopped, Claude Code changed: /home/a/.claude/skills/openvaultdb (already there, now managed by OVDB). Your copy is kept at /home/a/.claude/skills/.cli-helpers-skills-adopted-backup/x/openvaultdb',
            next: [],
          },
        }),
    })
    const wrapper = mount(SkillsScreen, { attachTo: window.document.body })
    await flushPromises()
    await wrapper.get('[data-testid="install-skill"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toContain('Before it stopped, Claude Code changed: /home/a/.claude/skills/openvaultdb')
    expect(wrapper.get('[role="alert"]').text()).toContain('Your copy is kept at /home/a/.claude/skills/.cli-helpers-skills-adopted-backup/x/openvaultdb')
    expect(reads).toBe(2)
  })

  it('shows a refusal with what to do', async () => {
    window.history.replaceState({}, '', '/skills?skill=openvaultdb')
    installFetch({
      ...defaultRoutes,
      'GET /api/local/v1/skills?adoptable=1&recovery=1': () => json(200, document()),
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
