// Explore data (capability 22, spec/features/explore-data-handoff): the
// intent-first menu, the prepared DataTug CLI connection (no token, always
// --no-policies), and DataTug.app's honest limitation with no control
// implying OVDB can open a database there.
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { resetConnection } from '../src/api'
import ExploreScreen from '../src/screens/ExploreScreen.vue'
import { defaultRoutes, installFetch, json } from './fakeServer'

enableAutoUnmount(afterEach)

beforeEach(() => {
  resetConnection()
  window.history.replaceState({}, '', '/explore')
})

afterEach(() => vi.unstubAllGlobals())

const context = { schema: 1, context: null, global: { database: 'notes', path: '/', scope: 'global' }, databases: ['notes'], next: [] }
const notesDemo = { schema: 1, app: 'todo', installed: false, location: '/x', app_path: '/apps/todo/', lists: [], next: [] }
const todoDemo = { ...notesDemo, installed: true, database: 'todo' }

const cliReady = {
  on_path: true,
  collection: 'lists',
  descriptor_path: '/home/a/.config/ovdb/explore/datatug/notes.json',
  descriptor: { baseUrl: 'http://127.0.0.1:6832', databaseId: 'notes', tokenEnv: 'OVDB_DATATUG_TOKEN', principalId: 'local-owner' },
  env_lines: [
    { name: 'OVDB_DATATUG_TOKEN', value: '<paste the token from: ovdb token create --db notes --scope read-only>' },
    { name: 'OVDB_DATATUG_TOKEN_BASE_URL', value: 'http://127.0.0.1:6832' },
    { name: 'OVDB_DATATUG_TOKEN_PRINCIPAL_ID', value: 'local-owner' },
  ],
  shell: 'sh',
  shell_text: 'export OVDB_DATATUG_TOKEN=<paste the token from: ovdb token create --db notes --scope read-only>',
  token_command: 'ovdb token create --db notes --scope read-only',
  query_command: 'datatug query run --db "openvaultdb:///home/a/.config/ovdb/explore/datatug/notes.json" \\\n  --from lists --as local-owner --no-policies --format json',
  next: [],
}

const cliMissing = { ...cliReady, on_path: false, install_commands: ['brew tap datatug/tap && brew install datatug', 'go install github.com/datatug/datatug-cli@latest'] }

describe('Explore data', () => {
  it('names the current database and presents both tools before any file is written', async () => {
    window.history.replaceState({}, '', '/explore?db=notes')
    const calls = installFetch({ ...defaultRoutes, 'GET /api/local/v1/demo': () => json(200, notesDemo) })
    const wrapper = mount(ExploreScreen, { attachTo: document.body })
    await flushPromises()
    expect(wrapper.get('h1').text()).toBe('Explore data')
    expect(wrapper.text()).toContain("Where would you like to explore notes's data?")
    expect(wrapper.get('[data-option="datatug-cli"]').text()).toContain('In the terminal with DataTug CLI')
    expect(wrapper.get('[data-option="datatug-app"]').text()).toContain('In the browser with DataTug.app')
    expect(calls.some((c) => c.method !== 'GET')).toBe(false)
  })

  it('resolves the current database from the console global default when no ?db is given', async () => {
    installFetch({ ...defaultRoutes, 'GET /api/local/v1/context': () => json(200, context), 'GET /api/local/v1/demo': () => json(200, notesDemo) })
    const wrapper = mount(ExploreScreen)
    await flushPromises()
    expect(wrapper.text()).toContain("Where would you like to explore notes's data?")
  })

  it('shows the demo-specific DataTug CLI copy for the installed demo database', async () => {
    window.history.replaceState({}, '', '/explore?db=todo')
    installFetch({ ...defaultRoutes, 'GET /api/local/v1/demo': () => json(200, todoDemo) })
    const wrapper = mount(ExploreScreen)
    await flushPromises()
    expect(wrapper.get('[data-option="datatug-cli"]').text()).toContain(
      "DataTug shows your two lists, not their items yet. See items in the TODO app, in Browse data, or with `ovdb list /lists/to-buy/items --db todo`.",
    )
  })

  it('prepares the DataTug CLI connection: four-key descriptor, no token, and --no-policies', async () => {
    window.history.replaceState({}, '', '/explore?db=notes')
    const calls = installFetch({
      ...defaultRoutes,
      'GET /api/local/v1/demo': () => json(200, notesDemo),
      'GET /api/local/v1/explore/datatug?db=notes': () => json(200, cliReady),
    })
    const wrapper = mount(ExploreScreen)
    await flushPromises()
    await wrapper.get('[data-option="datatug-cli"]').trigger('click')
    await flushPromises()
    expect(calls.find((c) => c.path.startsWith('/api/local/v1/explore/datatug'))?.path).toBe('/api/local/v1/explore/datatug?db=notes')
    const result = wrapper.get('[data-testid="explore-datatug-cli"]')
    expect(result.text()).toContain('datatug is on your PATH.')
    expect(result.text()).not.toContain('ovdb_') // never a token value
    expect(result.text()).toContain('--no-policies')
    expect(result.text()).toContain('ovdb token create --db notes --scope read-only')
  })

  it('shows install commands when datatug is not on PATH, and still prints the prepared command', async () => {
    window.history.replaceState({}, '', '/explore?db=notes')
    installFetch({
      ...defaultRoutes,
      'GET /api/local/v1/demo': () => json(200, notesDemo),
      'GET /api/local/v1/explore/datatug?db=notes': () => json(200, cliMissing),
    })
    const wrapper = mount(ExploreScreen)
    await flushPromises()
    await wrapper.get('[data-option="datatug-cli"]').trigger('click')
    await flushPromises()
    const result = wrapper.get('[data-testid="explore-datatug-cli"]')
    expect(result.text()).toContain('brew tap datatug/tap && brew install datatug')
    expect(result.text()).toContain('--no-policies') // the prepared command, for afterwards
  })

  it('DataTug.app states the honest limitation and offers no control implying it can open the database', async () => {
    window.history.replaceState({}, '', '/explore?db=notes')
    installFetch({ ...defaultRoutes, 'GET /api/local/v1/demo': () => json(200, notesDemo) })
    const wrapper = mount(ExploreScreen)
    await flushPromises()
    await wrapper.get('[data-option="datatug-app"]').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain("DataTug.app can't open an OpenVaultDB database directly yet.")
    expect(wrapper.find('[data-testid="datatug-cli-instead"]').text()).toBe('Use DataTug CLI instead')
    const openLink = wrapper.get('[data-testid="open-datatug-app"]')
    expect(openLink.attributes('href')).toBe('https://datatug.app')
    expect(openLink.text()).toBe('Open DataTug.app')
    expect(wrapper.findAll('button, a').map((el) => el.text())).not.toContain('Open database')
  })
})
