// Helpers that drive the ovdb binary started by global-setup.ts.
import { execFileSync } from 'node:child_process'

import type { Browser, BrowserContext } from '@playwright/test'

export const port = () => Number(process.env.OVDB_PORT)
export const primary = () => `http://ovdb.localhost:${port()}`
export const fallback = () => `http://127.0.0.1:${port()}`

export function ovdb(...args: string[]): string {
  return execFileSync(process.env.OVDB_E2E_BIN!, args, { env: process.env, encoding: 'utf8' })
}

export interface LoginLink {
  url: string
  fallback_url: string
}

/** A fresh single-use login link, as an AI assistant or `ovdb open` hands it out. */
export function loginLink(): LoginLink {
  return JSON.parse(ovdb('open', '--print-url', '--json')) as LoginLink
}

/** A new browser context signed in through a login link. */
export async function signedInContext(
  browser: Browser,
  options: Parameters<Browser['newContext']>[0] = {},
  host: 'primary' | 'fallback' = 'primary',
): Promise<BrowserContext> {
  const context = await browser.newContext(options)
  const page = await context.newPage()
  const link = loginLink()
  await page.goto(host === 'primary' ? link.url : link.fallback_url)
  await page.getByRole('heading', { name: 'What would you like to do?' }).waitFor()
  await page.close()
  return context
}
