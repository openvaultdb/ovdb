import { describe, expect, it } from 'vitest'

import { t } from '../src/copy'

describe('t()', () => {
  it('renders a known key', () => {
    expect(t('home.menu.try_demo')).toBe('Try a demo')
  })

  it('substitutes a {name} placeholder', () => {
    const rendered = t('server.also_at', { address: 'http://127.0.0.1:6832' })
    expect(rendered).toBe('Also at http://127.0.0.1:6832')
  })

  it('leaves an unsubstituted placeholder in place', () => {
    expect(t('server.also_at')).toContain('{address}')
  })

  it('throws on an unknown key', () => {
    expect(() => t('this.key.does.not.exist')).toThrowError(/Missing copy key/)
  })
})
