import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { resetConnection } from '../src/api'
import { defaultLocation, filterEngines } from '../src/engines'
import { currentPath } from '../src/router'
import CreateScreen from '../src/screens/CreateScreen.vue'
import DatabasesScreen from '../src/screens/DatabasesScreen.vue'
import { databaseRoutes, defaultRoutes, engines, installFetch, json, type Handler } from './fakeServer'

enableAutoUnmount(afterEach)

beforeEach(() => {
  resetConnection()
  window.history.replaceState({}, '', '/databases/new')
  currentPath.value = '/databases/new'
})

afterEach(() => {
  vi.unstubAllGlobals()
})

const routes = (extra: Record<string, Handler> = {}) => ({ ...defaultRoutes, ...databaseRoutes, ...extra })

async function openPicker(extra: Record<string, Handler> = {}) {
  const calls = installFetch(routes(extra))
  const wrapper = mount(CreateScreen, { attachTo: document.body })
  await flushPromises()
  return { wrapper, calls }
}

const shown = (wrapper: ReturnType<typeof mount>) => wrapper.findAll('[data-engine]').map((b) => b.attributes('data-engine'))

describe('storage picker', () => {
  it('shows pinned inGitDB and SQLite, then the rest in the server’s order', async () => {
    const { wrapper } = await openPicker()
    expect(wrapper.get('h2').text()).toBe('Where should OVDB keep your data?')
    expect(shown(wrapper)).toEqual(['ingitdb', 'sqlite', 'firestore', 'mysql', 'postgres'])
    expect(wrapper.get('[data-group="pinned"]').findAll('[data-engine]').map((b) => b.attributes('data-engine'))).toEqual(['ingitdb', 'sqlite'])
    expect(wrapper.get('[data-engine="sqlite"]').text()).toContain('You describe your data (a schema) before storing records.')
  })

  it('filters by id, name and description, keeping the order (AC:pinned-then-alphabetical)', async () => {
    const { wrapper } = await openPicker()
    await wrapper.get('input').setValue('sql')
    expect(shown(wrapper)).toEqual(['sqlite', 'mysql', 'postgres'])
    await wrapper.get('input').setValue('GIT HISTORY')
    expect(shown(wrapper)).toEqual(['ingitdb'])
    await wrapper.get('input').setValue('oracle')
    expect(shown(wrapper)).toEqual([])
    expect(wrapper.text()).toContain('Nothing matches "oracle".')
    expect(filterEngines(engines.engines as never, ' Sql ').map((e) => e.id)).toEqual(['sqlite', 'mysql', 'postgres'])
  })

  it('moves between choices with the arrow keys', async () => {
    const { wrapper } = await openPicker()
    const first = wrapper.get('[data-engine="ingitdb"]')
    ;(first.element as HTMLElement).focus()
    await first.trigger('keydown', { key: 'ArrowDown' })
    expect(document.activeElement).toBe(wrapper.get('[data-engine="sqlite"]').element)
    await wrapper.get('[data-engine="sqlite"]').trigger('keydown', { key: 'ArrowUp' })
    expect(document.activeElement).toBe(first.element)
  })

  it('shows PostgreSQL’s manifest steps and no connection fields (AC:postgres-is-manifest-only)', async () => {
    const { wrapper, calls } = await openPicker()
    await wrapper.get('[data-engine="postgres"]').trigger('click')
    const steps = wrapper.get('[data-testid="manifest-steps"]')
    expect(steps.text()).toContain('Set this up with a manifest file')
    expect(steps.text()).toContain('ovdb init --engine postgres --id <name>')
    expect(steps.text()).toContain('ovdb databases connect --manifest <absolute path>')
    expect(steps.get('a[href="/databases/connect?engine=postgres"]').text()).toBe('Connect with a manifest file')
    expect(steps.get('a[href^="https://"]').attributes('href')).toBe('https://github.com/openvaultdb/openvaultdb-go#manifest-examples')
    expect(wrapper.findAll('input')).toHaveLength(0)
    expect(calls.filter((c) => c.method !== 'GET')).toEqual([])
    await wrapper.findAll('button').find((b) => b.text() === 'Change storage')!.trigger('click')
    expect(shown(wrapper)).toHaveLength(5)
  })
})

describe('create form and result', () => {
  it('suggests the location under the data home as the name is typed, and sends it absolute', async () => {
    const created = {
      schema: 1,
      database: { id: 'notes', engine: 'ingitdb', location: '/home/a/ovdb/notes', state: 'mounted', manifest: '/home/a/.config/ovdb/databases/notes.yaml' },
      next: [
        { label: 'See your databases', command: 'ovdb databases', action: 'databases' },
        { label: 'Done', action: 'done' },
      ],
    }
    const { wrapper, calls } = await openPicker({ 'POST /api/local/v1/databases': () => json(201, created) })
    await wrapper.get('[data-engine="ingitdb"]').trigger('click')
    expect(document.activeElement).toBe(wrapper.findAll('input')[0].element)
    const [name, location] = wrapper.findAll('input')
    await name.setValue('notes')
    expect((location.element as HTMLInputElement).value).toBe('/home/a/ovdb/notes')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(calls.at(-1)).toEqual({ method: 'POST', path: '/api/local/v1/databases', body: { id: 'notes', engine: 'ingitdb', path: '/home/a/ovdb/notes' } })

    const result = wrapper.get('[data-testid="create-result"]')
    expect(result.text()).toContain('Created database notes')
    expect(result.text()).toContain('Stored in /home/a/ovdb/notes/ as readable files with Git history.')
    expect(result.get('a[href="/databases"]').text()).toBe('See your databases')
    await result.get('a[href="/"]').trigger('click', { button: 0 })
    expect(currentPath.value).toBe('/')
  })

  it('keeps a location the person typed, and uses .sqlite for SQLite', async () => {
    const { wrapper } = await openPicker()
    await wrapper.get('[data-engine="sqlite"]').trigger('click')
    const [name, location] = wrapper.findAll('input')
    await name.setValue('shop')
    expect((location.element as HTMLInputElement).value).toBe('/home/a/ovdb/shop.sqlite')
    await location.setValue('/srv/shop.db')
    await name.setValue('shop2')
    expect((location.element as HTMLInputElement).value).toBe('/srv/shop.db')
    expect(defaultLocation('C:\\Users\\a\\ovdb', 'sqlite', 'shop')).toBe('C:\\Users\\a\\ovdb\\shop.sqlite')
  })

  it('shows SQLite’s schema step first, with the docs link', async () => {
    const created = {
      schema: 1,
      database: { id: 'shop', engine: 'sqlite', location: '/home/a/ovdb/shop.sqlite', state: 'mounted', manifest: '/m/shop.yaml' },
      next: [
        { label: 'Describe your data first: add a collection to schemas in /m/shop.yaml, then restart the OVDB server', command: 'ovdb server restart' },
        { label: 'Read how to describe a schema: https://github.com/openvaultdb/openvaultdb-go#schema-modes' },
        { label: 'See your databases', command: 'ovdb databases', action: 'databases' },
        { label: 'Done', action: 'done' },
      ],
    }
    const { wrapper } = await openPicker({ 'POST /api/local/v1/databases': () => json(201, created) })
    await wrapper.get('[data-engine="sqlite"]').trigger('click')
    await wrapper.findAll('input')[0].setValue('shop')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    const items = wrapper.get('[data-testid="create-result"]').findAll('li')
    expect(items[0].text()).toContain('Describe your data first')
    expect(items[0].text()).toContain('ovdb server restart')
    expect(items[1].get('a').attributes('href')).toBe('https://github.com/openvaultdb/openvaultdb-go#schema-modes')
    expect(wrapper.text()).toContain('Stored in /home/a/ovdb/shop.sqlite as one SQLite file, with a placeholder collection example to rename.')
    expect(wrapper.text()).not.toMatch(/ovdb (add|set) /)
  })

  it('renders the server’s problem and offers the in-page remedies (AC:create-refuses-overwrite)', async () => {
    const problem = {
      schema: 1,
      error: {
        code: 'location_not_empty',
        message: "Couldn't create the database",
        reason: '/home/a/ovdb/notes already has files in it. OVDB never writes over existing data.',
        next: [
          { label: 'Choose another location', command: 'ovdb databases create notes --path <another absolute path>', action: 'edit_location' },
          { label: 'Done', action: 'done' },
        ],
      },
    }
    const { wrapper } = await openPicker({ 'POST /api/local/v1/databases': () => json(409, problem) })
    await wrapper.get('[data-engine="ingitdb"]').trigger('click')
    await wrapper.findAll('input')[0].setValue('notes')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    const alert = wrapper.get('[role="alert"]')
    expect(alert.text()).toContain("Couldn't create the database")
    expect(alert.text()).toContain('ovdb databases create notes --path <another absolute path>')
    expect(alert.text()).not.toContain('Done')
    // The location field carries the reason; the remedy button focuses it.
    const location = wrapper.findAll('input')[1]
    expect(location.attributes('aria-invalid')).toBe('true')
    expect(wrapper.text()).toContain('/home/a/ovdb/notes already has files in it.')
    expect(alert.text()).not.toContain('Why:')
    await alert.findAll('button').find((b) => b.text() === 'Choose another location')!.trigger('click')
    expect(document.activeElement).toBe(location.element)
  })

  it('fills in the suggested name when a name is taken (F8)', async () => {
    const problem = {
      schema: 1,
      error: {
        code: 'already_exists',
        message: "Couldn't create the database",
        reason: 'A database named notes is already registered.',
        next: [
          { label: 'Use the name notes-2 instead', command: 'ovdb databases create notes-2', action: 'edit_name' },
          { label: 'See your databases', command: 'ovdb databases', action: 'databases' },
        ],
      },
    }
    const { wrapper } = await openPicker({ 'POST /api/local/v1/databases': () => json(409, problem) })
    await wrapper.get('[data-engine="ingitdb"]').trigger('click')
    await wrapper.findAll('input')[0].setValue('notes')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    const [name, location] = wrapper.findAll('input')
    expect(name.attributes('aria-invalid')).toBe('true')
    await wrapper.get('[role="alert"]').findAll('button').find((b) => b.text() === 'Use the name notes-2 instead')!.trigger('click')
    expect((name.element as HTMLInputElement).value).toBe('notes-2')
    expect((location.element as HTMLInputElement).value).toBe('/home/a/ovdb/notes-2')
    expect(document.activeElement).toBe(name.element)
  })
})

describe('Databases', () => {
  const listed = {
    schema: 1,
    databases: [
      { id: 'crm', engine: 'postgres', location: 'connection from $CRM_DSN', state: 'needs_attention', reason: 'failed to open Postgres via $CRM_DSN: dial error', manifest: '/m/crm.yaml' },
      { id: 'notes', engine: 'ingitdb', location: '/home/a/ovdb/notes', state: 'mounted', manifest: '/m/notes.yaml' },
    ],
    next: [],
  }

  it('lists state, storage, location and why a database needs attention', async () => {
    installFetch(routes({ 'GET /api/local/v1/databases': () => json(200, listed) }))
    const wrapper = mount(DatabasesScreen)
    await flushPromises()
    const notes = wrapper.get('[data-database="notes"]')
    expect(notes.text()).toContain('Ready')
    expect(notes.text()).toContain('inGitDB')
    expect(notes.text()).toContain('/home/a/ovdb/notes')
    const crm = wrapper.get('[data-database="crm"]')
    expect(crm.text()).toContain('Needs attention')
    expect(crm.text()).toContain('Why: failed to open Postgres via $CRM_DSN')
    expect(crm.text()).toContain('/m/crm.yaml')
  })

  it('removes only after confirming, says the data stays, and refreshes', async () => {
    let current = listed
    const calls = installFetch(
      routes({
        'GET /api/local/v1/databases': () => json(200, current),
        'DELETE /api/local/v1/databases/notes': () => {
          current = { ...listed, databases: [listed.databases[0]] }
          return json(200, { schema: 1, database: { id: 'notes', engine: 'ingitdb', location: '/home/a/ovdb/notes', manifest: '/m/notes.yaml' }, next: [] })
        },
      }),
    )
    const wrapper = mount(DatabasesScreen, { attachTo: document.body })
    await flushPromises()
    await wrapper.get('[data-remove="notes"]').trigger('click')
    const confirm = wrapper.get('[data-confirm="notes"]')
    expect(confirm.text()).toContain('Remove notes from OVDB?')
    expect(confirm.text()).toContain('Its data stays where it is: /home/a/ovdb/notes')
    expect(document.activeElement?.textContent?.trim()).toBe('Keep it')
    expect(calls.some((c) => c.method === 'DELETE')).toBe(false)

    await confirm.findAll('button').find((b) => b.text() === 'Keep it')!.trigger('click')
    expect(wrapper.find('[data-confirm="notes"]').exists()).toBe(false)

    await wrapper.get('[data-remove="notes"]').trigger('click')
    await wrapper.get('[data-confirm="notes"]').findAll('button').find((b) => b.text() === 'Remove')!.trigger('click')
    await flushPromises()
    expect(wrapper.get('[role="status"]').text()).toContain('Removed database notes from OVDB')
    expect(wrapper.text()).toContain('Your data is still in /home/a/ovdb/notes.')
    expect(wrapper.find('[data-database="notes"]').exists()).toBe(false)
  })

  it('reloads a database and shows the state it ends in', async () => {
    const calls = installFetch(
      routes({
        'GET /api/local/v1/databases': () => json(200, listed),
        'POST /api/local/v1/databases/crm/reload': () =>
          json(200, { schema: 1, database: { ...listed.databases[0], reason: 'still unreachable' }, next: [] }),
      }),
    )
    const wrapper = mount(DatabasesScreen)
    await flushPromises()
    expect(wrapper.get('[data-database="crm"]').text()).toContain('/m/crm.yaml')
    await wrapper.get('[data-reload="crm"]').trigger('click')
    await flushPromises()
    expect(calls.some((c) => c.method === 'POST' && c.path === '/api/local/v1/databases/crm/reload')).toBe(true)
    const notice = wrapper.get('[role="alert"]')
    expect(notice.text()).toContain('Reloaded database crm · Needs attention')
    expect(notice.text()).toContain('Why: still unreachable')
  })

  it('says when there are none', async () => {
    installFetch(routes({ 'GET /api/local/v1/databases': () => json(200, { schema: 1, databases: [], next: [] }) }))
    const wrapper = mount(DatabasesScreen)
    await flushPromises()
    expect(wrapper.get('[data-testid="databases-empty"]').text()).toBe('No databases yet.')
    expect(wrapper.get('a[href="/databases/new"]').text()).toBe('Create a database')
  })
})
