// Try a demo in the web console (capabilities 18 and 19,
// todo-demo#REQ:demo-next-actions): where the lists will be stored before
// installing, then the server's next actions with Open TODO app first.
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { resetConnection } from '../src/api'
import DemoScreen from '../src/screens/DemoScreen.vue'
import { defaultRoutes, installFetch, json } from './fakeServer'

enableAutoUnmount(afterEach)

beforeEach(() => resetConnection())
afterEach(() => vi.unstubAllGlobals())

const notInstalled = {
  schema: 1,
  app: 'todo',
  installed: false,
  location: '/home/a/ovdb/demos/todo',
  app_path: '/apps/todo/',
  lists: ['/lists/to-buy', '/lists/to-watch'],
  next: [{ label: 'Install the TODO demo', command: 'ovdb demo install --yes', action: 'install_demo' }],
}
const installed = {
  ...notInstalled,
  installed: true,
  database: 'todo',
  state: 'mounted',
  next: [
    { label: 'Open TODO app', command: 'ovdb demo open', action: 'open_app' },
    { label: 'Install TODO AI skill', command: 'ovdb skills install todo-demo', action: 'install_skill' },
    { label: 'Done', action: 'done' },
  ],
}

describe('Try a demo', () => {
  it('shows where the lists will go, installs, and offers Open TODO app then Done', async () => {
    const calls = installFetch({
      ...defaultRoutes,
      'GET /api/local/v1/demo': () => json(200, notInstalled),
      'POST /api/local/v1/demo/install': () => json(201, installed),
    })
    const wrapper = mount(DemoScreen, { attachTo: document.body })
    await flushPromises()
    expect(wrapper.get('h1').text()).toBe('Try a demo')
    expect(wrapper.get('[data-testid="demo-location"]').text()).toBe('The lists will be stored as readable files in /home/a/ovdb/demos/todo')
    expect(calls.some((c) => c.method === 'POST')).toBe(false) // nothing written before asking

    await wrapper.get('[data-testid="install-demo"]').trigger('click')
    await flushPromises()
    expect(calls.find((c) => c.method === 'POST')).toEqual({ method: 'POST', path: '/api/local/v1/demo/install', body: {} })
    const result = wrapper.get('[data-testid="demo-result"]')
    expect(result.text()).toContain('The TODO demo is ready')
    expect(result.text()).toContain('Two lists, To buy and To watch, are stored as files in /home/a/ovdb/demos/todo.')
    expect(result.findAll('a').map((a) => [a.text(), a.attributes('href')])).toEqual([
      ['Open TODO app', '/apps/todo/'],
      ['Install TODO AI skill', '/skills?skill=todo-demo&from=/demo'],
      ['Done', '/'],
    ])
    expect(result.text()).toContain('ovdb demo open')
    expect(result.text()).toContain('ovdb skills install todo-demo')
    expect(document.activeElement).toBe(result.element)
  })

  it('says the demo is already installed', async () => {
    installFetch({ ...defaultRoutes, 'GET /api/local/v1/demo': () => json(200, installed) })
    const wrapper = mount(DemoScreen)
    await flushPromises()
    expect(wrapper.get('[data-testid="demo-result"]').text()).toContain('The TODO demo is already installed')
    expect(wrapper.find('[data-testid="install-demo"]').exists()).toBe(false)
  })

  it('shows a refusal with what to do', async () => {
    installFetch({
      ...defaultRoutes,
      'GET /api/local/v1/demo': () => json(200, notInstalled),
      'POST /api/local/v1/demo/install': () =>
        json(409, {
          schema: 1,
          error: {
            code: 'already_exists',
            message: "Couldn't install the TODO demo",
            reason: 'A database named todo already exists at /home/a/ovdb/todo.',
            next: [{ label: 'Install it under another name', command: 'ovdb demo install --id todo-demo', action: 'edit_name' }],
          },
        }),
    })
    const wrapper = mount(DemoScreen)
    await flushPromises()
    await wrapper.get('[data-testid="install-demo"]').trigger('click')
    await flushPromises()
    const alert = wrapper.get('[role="alert"]')
    expect(alert.text()).toContain("Couldn't install the TODO demo")
    expect(alert.text()).toContain('ovdb demo install --id todo-demo')
  })
})
