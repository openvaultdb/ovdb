// OvCommand's copy button (review-inc-7.md F4): every printed command,
// across every screen that shows one, gets a copy action, since a long
// command (a DataTug CLI query, a manifest path) may be impractical to
// select by hand.
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'

import OvCommand from '../src/components/OvCommand.vue'

enableAutoUnmount(afterEach)
afterEach(() => vi.unstubAllGlobals())

describe('OvCommand', () => {
  it('shows the command text and copies it to the clipboard on click', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    vi.stubGlobal('navigator', { clipboard: { writeText } })
    const wrapper = mount(OvCommand, { props: { command: 'ovdb explore datatug-cli --db todo' } })
    expect(wrapper.text()).toContain('ovdb explore datatug-cli --db todo')
    const button = wrapper.get('button')
    expect(button.attributes('aria-label') ?? button.text()).toMatch(/copy/i)
    await button.trigger('click')
    await flushPromises()
    expect(writeText).toHaveBeenCalledWith('ovdb explore datatug-cli --db todo')
    expect(wrapper.text()).toMatch(/copied/i)
  })
})
