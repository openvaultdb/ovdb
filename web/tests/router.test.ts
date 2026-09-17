import { describe, expect, it } from 'vitest'

import routes from '../routes.json'
import { currentPath, screen } from '../src/router'

describe('router', () => {
  it('maps every route in routes.json to its screen', () => {
    for (const route of routes) {
      currentPath.value = route.path
      expect(screen.value).toBe(route.screen)
    }
    currentPath.value = '/missing'
    expect(screen.value).toBe('not-found')
  })
})
