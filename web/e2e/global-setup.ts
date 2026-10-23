// Builds ovdb (unless OVDB_E2E_BIN is set), starts it detached in a fresh
// temporary OVDB home on a free port, and stops it and removes the home
// afterwards. Workers inherit the environment set here.
import { execFileSync } from 'node:child_process'
import { createSocket, type Socket } from 'node:dgram'
import { existsSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import { createServer } from 'node:net'
import { tmpdir } from 'node:os'
import { delimiter, dirname, join } from 'node:path'
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
// (49152-65535), and are leased the way Go tests lease theirs
// (internal/porttest): the lease on TCP port N is a UDP socket bound to
// 127.0.0.1:N and [::1]:N, held until teardown. The UDP table is the OS's own,
// so the lease excludes every other holder on the machine, Go test processes
// included (the same range is theirs), and goes when this process dies; it does
// not stop the server binding the number for TCP. libuv sets no SO_REUSEADDR
// on a dgram socket unless asked (reuseAddr), which this does not.
const firstPort = 20000
const portCount = 4000

// holdUdp binds a UDP socket at host:port: 'held' with the socket, 'taken'
// when something else has the number, 'absent' for a host the machine does not
// have (no IPv6 loopback).
function holdUdp(port: number, host: string, type: 'udp4' | 'udp6'): Promise<{ state: 'held'; socket: Socket } | { state: 'taken' | 'absent' }> {
  return new Promise((resolve) => {
    const socket = createSocket(type)
    socket.once('error', (error: NodeJS.ErrnoException) => {
      socket.close()
      resolve({ state: error.code === 'EADDRNOTAVAIL' || error.code === 'EAFNOSUPPORT' ? 'absent' : 'taken' })
    })
    socket.bind(port, host, () => resolve({ state: 'held', socket }))
  })
}

// listens reports whether host:port can be bound for TCP; a host the machine
// does not have counts as free.
function listens(port: number, host: string): Promise<boolean> {
  return new Promise((resolve) => {
    const server = createServer()
    server.once('error', (error: NodeJS.ErrnoException) => resolve(error.code !== 'EADDRINUSE' && error.code !== 'EACCES'))
    server.listen(port, host, () => server.close(() => resolve(true)))
  })
}

/** A leased port and the sockets that hold it. */
async function leasePort(): Promise<{ port: number; release: () => void }> {
  const start = Math.floor(Math.random() * portCount)
  for (let i = 0; i < portCount; i++) {
    const port = firstPort + ((start + i) % portCount)
    const held: Socket[] = []
    const release = () => held.forEach((socket) => socket.close())
    let ok = true
    for (const [host, type] of [['127.0.0.1', 'udp4'], ['::1', 'udp6']] as const) {
      const result = await holdUdp(port, host, type)
      if (result.state === 'held') held.push(result.socket)
      else if (result.state === 'taken') ok = false
    }
    if (ok && (await listens(port, '127.0.0.1')) && (await listens(port, '::1'))) return { port, release }
    release()
  }
  throw new Error(`no free port in ${firstPort}-${firstPort + portCount - 1} for the e2e server`)
}

// Browser openers that do nothing, first on PATH for every process the tests
// start (the workers inherit it): ovdb opens a browser for `ovdb open` and
// `ovdb cloud login`, and on a desktop that would be the person's real one.
// Each records its call in calls.log next to it.
function installOpenerStubs(base: string): string {
  const dir = join(base, 'stubbin')
  mkdirSync(dir, { recursive: true })
  for (const name of ['open', 'xdg-open', 'rundll32']) {
    if (process.platform === 'win32') {
      writeFileSync(join(dir, `${name}.cmd`), '@echo off\r\necho %~n0 %* >> "%~dp0calls.log"\r\nexit /b 0\r\n')
    } else {
      writeFileSync(join(dir, name), '#!/bin/sh\necho "$(basename "$0") $*" >> "$(dirname "$0")/calls.log"\nexit 0\n', { mode: 0o755 })
    }
  }
  return dir
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
  const lease = await leasePort()
  const port = lease.port
  const stubs = installOpenerStubs(base)
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
    // The openers every child process finds first (see installOpenerStubs).
    PATH: stubs + delimiter + (process.env.PATH ?? ''),
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
      lease.release()
      rmSync(base, { recursive: true, force: true })
    }
  }
}
