import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'

import App from '../src/App.vue'

describe('console App placeholder', () => {
  it('renders the placeholder title and every implemented home menu item', () => {
    const wrapper = mount(App)
    expect(wrapper.text()).toContain('OVDB console')
    expect(wrapper.text()).toContain('Try a demo')
    expect(wrapper.text()).toContain('Create a database')
    expect(wrapper.text()).toContain('Connect an existing database')
    expect(wrapper.text()).toContain('Start the OVDB server')
  })
})
