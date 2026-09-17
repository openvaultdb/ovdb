import { describe, expect, it } from 'vitest'

import { t } from '../src/copy'

describe('t()', () => {
  it('renders a known key', () => {
    expect(t('home.menu.try_demo')).toBe('Try a demo')
  })

  it('substitutes a {name} placeholder', () => {
    const rendered = t('console.placeholder.body', { name: 'ovdb' })
    expect(rendered).toContain('ovdb')
    expect(rendered).not.toContain('{name}')
  })

  it('leaves an unsubstituted placeholder in place', () => {
    expect(t('console.placeholder.body')).toContain('{name}')
  })

  it('throws on an unknown key', () => {
    expect(() => t('this.key.does.not.exist')).toThrowError(/Missing copy key/)
  })
})
