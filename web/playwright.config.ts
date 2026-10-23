import { defineConfig, devices } from '@playwright/test'

// Browser journeys against a real ovdb binary in an isolated temporary
// OVDB home (spec/features/configuration-parity#REQ:ci-matrix). Build the
// console first (`pnpm build`) so the binary embeds it; e2e/global-setup.ts
// builds the binary unless OVDB_E2E_BIN names one.
export default defineConfig({
  testDir: 'e2e',
  globalSetup: './e2e/global-setup.ts',
  fullyParallel: false,
  workers: 1,
  retries: 1,
  reporter: process.env.CI ? [['list'], ['github']] : 'list',
  outputDir: 'test-results',
  use: {
    ...devices['Desktop Chrome'],
    trace: 'retain-on-failure',
  },
})
