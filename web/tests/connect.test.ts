import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { resetConnection } from '../src/api'
import { nameFromLocation } from '../src/engines'
import { currentPath } from '../src/router'
import ConnectScreen from '../src/screens/ConnectScreen.vue'
import { databaseRoutes, defaultRoutes, installFetch, json, type Handler } from './fakeServer'

enableAutoUnmount(afterEach)

beforeEach(() => {
  resetConnection()
  window.history.replaceState({}, '', '/databases/connect')
  currentPath.value = '/databases/connect'
})

afterEach(() => {
  vi.unstubAllGlobals()
})

const routes = (extra: Record<string, Handler> = {}) => ({ ...defaultRoutes, ...databaseRoutes, ...extra })

async function open(extra: Record<string, Handler> = {}) {
  const calls = installFetch(routes(extra))
  const wrapper = mount(ConnectScreen, { attachTo: document.body })
  await flushPromises()
  return { wrapper, calls }
}

const shown = (wrapper: ReturnType<typeof mount>) => wrapper.findAll('[data-engine]').map((b) => b.attributes('data-engine'))
const button = (wrapper: ReturnType<typeof mount>, text: string) => wrapper.findAll('button').find((b) => b.text() === text)!

const connected = {
  schema: 1,
  database: { id: 'journal', engine: 'ingitdb', location: '/home/a/journal', state: 'mounted', manifest: '/home/a/.config/ovdb/databases/journal.yaml' },
  next: [
    { label: 'Browse data', command: 'ovdb list / --db journal', action: 'browse' },
    { label: 'Use it in this project', command: 'ovdb use journal', action: 'use' },
    { label: 'See your databases', command: 'ovdb databases', action: 'databases' },
    { label: 'Done', action: 'done' },
  ],
}

describe('Connect an existing database', () => {
  it('lists the catalogue in the server’s order, then Connect with a manifest file', async () => {
    const { wrapper } = await open()
    expect(wrapper.get('h2').text()).toBe('What would you like to connect?')
    expect(shown(wrapper)).toEqual(['ingitdb', 'sqlite', 'firestore', 'mysql', 'postgres', 'manifest'])
    await wrapper.get('input').setValue('sql')
    expect(shown(wrapper)).toEqual(['sqlite', 'mysql', 'postgres', 'manifest'])
    expect(wrapper.get('[data-engine="manifest"]').text()).toContain('Any storage: inGitDB, SQLite, Firestore, MySQL or PostgreSQL.')
  })

  it('connects a folder, naming it after the folder, and shows the result with Browse data (AC:result-next-actions)', async () => {
    const { wrapper, calls } = await open({ 'POST /api/local/v1/databases/connect': () => json(201, connected) })
    await wrapper.get('[data-engine="ingitdb"]').trigger('click')
    const [location, name] = wrapper.findAll('input')
    expect(document.activeElement).toBe(location.element)
    await location.setValue('/home/a/journal')
    expect((name.element as HTMLInputElement).value).toBe('journal')
    await name.setValue('diary')
    await location.setValue('/home/a/other')
    expect((name.element as HTMLInputElement).value).toBe('diary')
    await name.setValue('journal')
    await location.setValue('/home/a/journal')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(calls.at(-1)).toEqual({ method: 'POST', path: '/api/local/v1/databases/connect', body: { id: 'journal', engine: 'ingitdb', path: '/home/a/journal' } })

    const result = wrapper.get('[data-testid="connect-result"]')
    expect(result.text()).toContain('Connected database journal')
    expect(result.text()).toContain('Your data stays where it is: /home/a/journal')
    expect(result.get('a[href="/browse/journal"]').text()).toBe('Browse data')
    expect(result.get('a[href="/databases"]').text()).toBe('See your databases')
    expect(result.get('[data-testid="use-as-default"]').text()).toBe('Use as default')
    expect(result.get('a[href="/"]').text()).toBe('Done')
    await result.get('a[href="/browse/journal"]').trigger('click', { button: 0 })
    expect(currentPath.value).toBe('/browse/journal')
  })

  it('shows a manifest-only engine’s steps and asks only for the manifest file (AC:connect-postgres-manifest)', async () => {
    const problem = {
      schema: 1,
      error: {
        code: 'storage_unavailable',
        message: "Couldn't connect the database",
        reason: "CRM_DSN isn't set in the OVDB server's environment, so OVDB can't reach this database.",
        next: [
          { label: 'Set CRM_DSN and run `ovdb server restart` from that shell', command: 'ovdb server restart' },
          { label: 'Choose another manifest file', command: 'ovdb databases connect --manifest <absolute path>', action: 'edit_manifest' },
        ],
      },
    }
    const { wrapper, calls } = await open({ 'POST /api/local/v1/databases/connect': () => json(503, problem) })
    await wrapper.get('[data-engine="postgres"]').trigger('click')
    const steps = wrapper.get('[data-testid="manifest-steps"]')
    expect(steps.text()).toContain('ovdb init --engine postgres --id <name>')
    const inputs = wrapper.findAll('input')
    expect(inputs).toHaveLength(1)
    expect(wrapper.text()).toContain('Manifest file')
    expect(wrapper.text()).not.toMatch(/password|connection string/i)
    await inputs[0].setValue('/home/a/crm.yaml')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(calls.at(-1)).toEqual({ method: 'POST', path: '/api/local/v1/databases/connect', body: { manifest: '/home/a/crm.yaml' } })
    const alert = wrapper.get('[role="alert"]')
    expect(alert.text()).toContain('Set CRM_DSN and run ovdb server restart from that shell')
    expect(inputs[0].attributes('aria-invalid')).toBe('true')
    await button(wrapper, 'Choose another manifest file').trigger('click')
    expect(document.activeElement).toBe(inputs[0].element)
    await button(wrapper, 'Change storage').trigger('click')
    expect(shown(wrapper)).toHaveLength(6)
  })

  it('opens the manifest step for the engine Create linked from', async () => {
    window.history.replaceState({}, '', '/databases/connect?engine=mysql')
    const { wrapper } = await open()
    expect(wrapper.get('[data-testid="chosen"]').text()).toBe('Storage: MySQL')
    expect(wrapper.get('[data-testid="manifest-steps"]').text()).toContain('ovdb init --engine mysql --id <name>')
  })

  it('puts a refused folder’s reason under the field (AC:connect-leaves-folder-untouched)', async () => {
    const problem = {
      schema: 1,
      error: {
        code: 'storage_unavailable',
        message: "Couldn't connect the database",
        reason: "/home/a/x.sqlite isn't a SQLite database file.",
        next: [{ label: 'Choose another location', command: 'ovdb databases connect x --engine sqlite --path <another absolute path>', action: 'edit_location' }],
      },
    }
    const { wrapper } = await open({ 'POST /api/local/v1/databases/connect': () => json(503, problem) })
    await wrapper.get('[data-engine="sqlite"]').trigger('click')
    expect(wrapper.text()).toContain('File')
    const [location] = wrapper.findAll('input')
    await location.setValue('/home/a/x.sqlite')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(location.attributes('aria-invalid')).toBe('true')
    expect(wrapper.text()).toContain("/home/a/x.sqlite isn't a SQLite database file.")
    expect(wrapper.get('[role="alert"]').text()).not.toContain('Why:')
  })

  it('suggests names the way the TUI does', () => {
    expect(nameFromLocation('/home/a/my-notes/')).toBe('my-notes')
    expect(nameFromLocation('C:\\Users\\a\\shop.sqlite')).toBe('shop')
    expect(nameFromLocation('/home/a/my notes')).toBe('')
  })
})
