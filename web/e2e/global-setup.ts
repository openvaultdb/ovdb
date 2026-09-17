// Builds ovdb (unless OVDB_E2E_BIN is set), starts it detached in a fresh
// temporary OVDB home on a free port, and stops it and removes the home
// afterwards. Workers inherit the environment set here.
import { execFileSync } from 'node:child_process'
import { existsSync, mkdirSync, mkdtempSync, rmSync } from 'node:fs'
import { createServer } from 'node:net'
import { tmpdir } from 'node:os'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

import { ovdbEnv } from './ovdb'

const webRoot = join(dirname(fileURLToPath(import.meta.url)), '..')

function freePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const server = createServer()
    server.once('error', reject)
    server.listen(0, '127.0.0.1', () => {
      const address = server.address()
      server.close(() => resolve(typeof address === 'object' && address ? address.port : 0))
    })
  })
}

export default async function globalSetup() {
  if (!existsSync(join(webRoot, 'dist', 'index.html'))) {
    throw new Error('web/dist is not built: run `pnpm -C web build` before the browser tests')
  }
  const base = mkdtempSync(join(tmpdir(), 'ovdb-e2e-'))
  let binary = process.env.OVDB_E2E_BIN
  if (!binary) {
    binary = join(base, process.platform === 'win32' ? 'ovdb.exe' : 'ovdb')
    execFileSync('go', ['build', '-o', binary, '.'], { cwd: join(webRoot, '..'), stdio: 'inherit' })
  }
  const port = await freePort()
  // A home of its own for the ovdb processes, where AI agent skills get
  // installed, with Claude Code "installed" and Codex not (e2e/ovdb.ts
  // ovdbEnv). Playwright itself keeps the real HOME, where its browsers are.
  const user = join(base, 'user')
  mkdirSync(join(user, '.claude'), { recursive: true })
  let gitConfig = process.env.GIT_CONFIG_GLOBAL ?? ''
  if (!gitConfig) {
    try {
      // Git keeps the person's identity for inGitDB commits.
      const origin = execFileSync('git', ['config', '--global', '--show-origin', '--get', 'user.email'], { encoding: 'utf8' })
      gitConfig = /^file:(\S+)/.exec(origin)?.[1] ?? ''
    } catch {
      // No global identity: nothing to keep.
    }
  }
  Object.assign(process.env, {
    OVDB_E2E_BIN: binary,
    OVDB_E2E_USER_HOME: user,
    OVDB_E2E_GIT_CONFIG: gitConfig,
    OVDB_HOME: join(base, 'home'),
    OVDB_RUNTIME_DIR: join(base, 'run'),
    OVDB_DATA_HOME: join(base, 'data'),
    OVDB_PORT: String(port),
    OVDB_NON_INTERACTIVE: '1',
    // `ovdb databases` keeps its legacy behaviour without the gate.
    OVDB_PREVIEW: '1',
  })
  execFileSync(binary, ['server', 'start', '--json'], { env: ovdbEnv(), stdio: 'pipe' })

  return () => {
    try {
      execFileSync(binary, ['server', 'stop', '--json'], { env: ovdbEnv(), stdio: 'pipe' })
    } finally {
      rmSync(base, { recursive: true, force: true })
    }
  }
}
