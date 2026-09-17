import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'

import TodoApp from '../apps/todo/App.vue'

describe('TODO app placeholder', () => {
  it('renders the placeholder title and body', () => {
    const wrapper = mount(TodoApp)
    expect(wrapper.text()).toContain('TODO app')
    expect(wrapper.text()).toContain('/apps/todo/')
  })
})
