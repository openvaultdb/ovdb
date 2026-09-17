// Usage statistics in the web console (telemetry-consent
// #REQ:consent-prompt-placement, REQ:pre-consent-buffer,
// first-run-onboarding#REQ:telemetry-asked-after-first-success): one prompt
// on the first successful Result, Turn on and No thanks with equal weight,
// the page's buffer posted only after Turn on.
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { resetConnection } from '../src/api'
import DemoScreen from '../src/screens/DemoScreen.vue'
import SettingsScreen from '../src/screens/SettingsScreen.vue'
import { bufferedUsage, loadUsage, recordStart, recordUsage, resetUsage } from '../src/usage'
import { defaultRoutes, installFetch, json } from './fakeServer'

enableAutoUnmount(afterEach)

beforeEach(() => {
  resetConnection()
  resetUsage()
})
afterEach(() => vi.unstubAllGlobals())

const status = (state: string, extra: Record<string, unknown> = {}) => ({
  schema: 1,
  telemetry: {
    state,
    sending: state === 'enabled',
    available: true,
    provider: 'PostHog (EU)',
    has_install_id: state === 'enabled',
    collected: ['Which setup steps you use and whether they succeed'],
    never_collected: ['Anything you type'],
    ...extra,
  },
  next: [],
})

const demo = {
  schema: 1,
  app: 'todo',
  installed: false,
  location: '/home/a/ovdb/demos/todo',
  app_path: '/apps/todo/',
  lists: [],
  next: [],
}

function routes(state = 'not_asked', extra: Record<string, unknown> = {}) {
  let current = state
  return {
    ...defaultRoutes,
    'GET /api/local/v1/telemetry': () => json(200, status(current, extra)),
    'PUT /api/local/v1/telemetry': (init: RequestInit) => {
      current = JSON.parse(String(init.body)).state
      return json(200, { ...status(current), changed: true })
    },
    'POST /api/local/v1/telemetry/events': () => json(200, { schema: 1, accepted: 1, sent: true }),
    'GET /api/local/v1/demo': () => json(200, demo),
    'POST /api/local/v1/demo/install': () => json(201, { ...demo, installed: true, database: 'todo' }),
  }
}

async function installDemo() {
  const wrapper = mount(DemoScreen, { attachTo: document.body })
  await flushPromises()
  await wrapper.get('[data-testid="install-demo"]').trigger('click')
  await flushPromises()
  return wrapper
}

describe('usage statistics prompt', () => {
  it('asks once after the first success, with equal choices, and posts the buffer only after Turn on', async () => {
    const calls = installFetch(routes())
    await loadUsage()
    recordStart()
    recordUsage({ event: 'onboarding_option_selected', option: 'demo' })

    const wrapper = await installDemo()
    const prompt = wrapper.get('[data-testid="usage-prompt"]')
    expect(prompt.text()).toContain('Help improve OpenVaultDB?')
    const on = wrapper.get('[data-testid="usage-turn-on"]')
    const off = wrapper.get('[data-testid="usage-no-thanks"]')
    expect(on.text()).toBe('Turn on')
    expect(off.text()).toBe('No thanks')
    expect(on.classes()).toEqual(off.classes()) // equal weight
    await wrapper.get('[data-testid="usage-details"]').trigger('click')
    expect(wrapper.get('[data-testid="usage-lists"]').text()).toContain('Anything you type')
    expect(calls.some((c) => c.path === '/api/local/v1/telemetry/events')).toBe(false)

    await on.trigger('click')
    await flushPromises()
    expect(calls.find((c) => c.method === 'PUT')).toEqual({ method: 'PUT', path: '/api/local/v1/telemetry', body: { state: 'enabled', confirmed_by_user: true } })
    const posted = calls.filter((c) => c.path === '/api/local/v1/telemetry/events')
    expect(posted).toHaveLength(1)
    expect((posted[0].body as { events: { event: string }[] }).events.map((e) => e.event)).toEqual([
      'onboarding_started',
      'onboarding_option_selected',
      'demo_installed',
    ])
    expect(wrapper.find('[data-testid="usage-prompt"]').exists()).toBe(false)
    expect(wrapper.text()).toContain('Usage statistics are on. Thank you.')
    wrapper.unmount()

    // A second success on the same page asks nothing.
    const again = await installDemo()
    expect(again.find('[data-testid="usage-prompt"]').exists()).toBe(false)
  })

  it('sends nothing after No thanks', async () => {
    const calls = installFetch(routes())
    await loadUsage()
    recordStart()
    const wrapper = await installDemo()
    await wrapper.get('[data-testid="usage-no-thanks"]').trigger('click')
    await flushPromises()
    expect(calls.find((c) => c.method === 'PUT')?.body).toEqual({ state: 'disabled' })
    expect(calls.some((c) => c.path === '/api/local/v1/telemetry/events')).toBe(false)
    expect(bufferedUsage()).toEqual([])
  })

  it('drops the buffer when dismissed and does not ask again', async () => {
    const calls = installFetch(routes())
    await loadUsage()
    recordStart()
    const wrapper = await installDemo()
    await wrapper.get('[data-testid="usage-dismiss"]').trigger('click')
    expect(wrapper.find('[data-testid="usage-prompt"]').exists()).toBe(false)
    expect(bufferedUsage()).toEqual([])
    expect(calls.some((c) => c.method === 'PUT' || c.path.endsWith('/events'))).toBe(false)
    wrapper.unmount()
    const again = await installDemo()
    expect(again.find('[data-testid="usage-prompt"]').exists()).toBe(false)
  })

  it('does not ask when this server is forced off, or before the state is known', async () => {
    installFetch(routes('not_asked', { reason: 'DO_NOT_TRACK', reason_text: 'Nothing is sent: DO_NOT_TRACK is set for this process.' }))
    const unknown = await installDemo()
    expect(unknown.find('[data-testid="usage-prompt"]').exists()).toBe(false)
    unknown.unmount()
    await loadUsage()
    const forced = await installDemo()
    expect(forced.find('[data-testid="usage-prompt"]').exists()).toBe(false)
  })
})

describe('onboarding_completed', () => {
  it('is recorded when the person chooses Done on a completed step (review L1)', async () => {
    installFetch({
      ...routes(),
      'POST /api/local/v1/demo/install': () => json(201, { ...demo, installed: true, database: 'todo', next: [{ label: 'Done', action: 'done' }] }),
    })
    const wrapper = await installDemo()
    await wrapper.findAll('a').find((a) => a.text() === 'Done')!.trigger('click', { button: 0 })
    expect(bufferedUsage().at(-1)).toEqual({ event: 'onboarding_completed', step: 'demo' })
  })
})

describe('Settings → Usage statistics', () => {
  it('shows the shared state and turns it off', async () => {
    const calls = installFetch({ ...routes('enabled', { reason_text: 'Usage statistics are unavailable in this build, so nothing is sent.' }), 'GET /api/local/v1/config': () => json(200, { schema: 1, config: { server: {} }, next: [] }) })
    const wrapper = mount(SettingsScreen)
    await flushPromises()
    expect(wrapper.get('[data-testid="usage-state"]').text()).toBe('Status: On')
    expect(wrapper.get('[data-testid="usage-reason"]').text()).toContain('unavailable in this build')
    expect(wrapper.get('[data-testid="usage-settings"]').text()).toContain('Anything you type')
    await wrapper.get('[data-testid="usage-settings-off"]').trigger('click')
    await flushPromises()
    expect(calls.find((c) => c.method === 'PUT')?.body).toEqual({ state: 'disabled' })
    expect(wrapper.get('[data-testid="usage-state"]').text()).toBe('Status: Off')
  })
})
