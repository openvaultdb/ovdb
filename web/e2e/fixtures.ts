// The Playwright `test` every spec uses: it fails a test when any page it
// opened reports a Content-Security-Policy violation, so a build that starts
// needing inline styles or scripts cannot pass unnoticed.
import { test as base, expect, type BrowserContext } from '@playwright/test'

export { expect }

async function watchCSP(context: BrowserContext, violations: string[]) {
  await context.addInitScript(() => {
    document.addEventListener('securitypolicyviolation', (event) => {
      console.error(`CSP violation: ${event.violatedDirective} blocked ${event.blockedURI}`)
    })
  })
  const listen = (page: import('@playwright/test').Page) =>
    page.on('console', (message) => {
      const text = message.text()
      if (message.type() === 'error' && /CSP violation|Content Security Policy/i.test(text)) {
        violations.push(`${page.url()}: ${text}`)
      }
    })
  context.pages().forEach(listen)
  context.on('page', listen)
}

export const test = base.extend<{ cspViolations: string[] }>({
  cspViolations: [
    async ({ browser, context }, use) => {
      const violations: string[] = []
      await watchCSP(context, violations)
      const newContext = browser.newContext.bind(browser)
      browser.newContext = async (options) => {
        const created = await newContext(options)
        await watchCSP(created, violations)
        return created
      }
      await use(violations)
      browser.newContext = newContext
      expect(violations, 'Content-Security-Policy violations').toEqual([])
    },
    { auto: true },
  ],
})
