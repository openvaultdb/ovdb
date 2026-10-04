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

// The server is the real ovdb binary, started below on a port named in the
// environment, and restarted on it by several journeys, so the port must be
// known beforehand and stay free while the server is down. A port from
// listen(0) is not: it comes from the range the operating system also gives to
// every other bind(0) and to the source side of every connection the browser
// makes, and is released until the server binds it. These come from a range
// below the ephemeral range of Linux (32768-60999), macOS and Windows
// (49152-65535); the same range, with a lease per test, is internal/porttest's.
const firstPort = 20000
const portCount = 4000

// listens reports whether host:port can be bound; a host the machine does not
// have (no IPv6) counts as free.
function listens(port: number, host: string): Promise<boolean> {
  return new Promise((resolve) => {
    const server = createServer()
    server.once('error', (error: NodeJS.ErrnoException) => resolve(error.code !== 'EADDRINUSE' && error.code !== 'EACCES'))
    server.listen(port, host, () => server.close(() => resolve(true)))
  })
}

async function freePort(): Promise<number> {
  const start = Math.floor(Math.random() * portCount)
  for (let i = 0; i < portCount; i++) {
    const port = firstPort + ((start + i) % portCount)
    if ((await listens(port, '127.0.0.1')) && (await listens(port, '::1'))) return port
  }
  throw new Error(`no free port in ${firstPort}-${firstPort + portCount - 1} for the e2e server`)
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
