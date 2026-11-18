import { defineConfig } from '@playwright/test'
import fs from 'node:fs'
import path from 'node:path'

function loadEnv(): Record<string, string> {
  const file = path.resolve(process.cwd(), '..', '.env')
  if (!fs.existsSync(file)) return {}
  const values: Record<string, string> = {}
  for (const line of fs.readFileSync(file, 'utf8').split(/\r?\n/)) {
    const match = line.match(/^\s*([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.*)\s*$/)
    if (!match) continue
    values[match[1]!] = match[2]!.replace(/^['"]|['"]$/g, '')
  }
  return values
}

const fileEnv = loadEnv()
for (const [key, value] of Object.entries(fileEnv)) {
  if (!process.env[key]) process.env[key] = value
}

export default defineConfig({
  testDir: './e2e',
  fullyParallel: false,
  workers: 1,
  retries: 0,
  timeout: 300_000,
  expect: {
    timeout: 25_000,
  },
  reporter: [['list']],
  use: {
    baseURL: process.env.E2E_BASE_URL ?? 'http://localhost',
    channel: 'chrome',
    headless: true,
    viewport: { width: 1440, height: 1000 },
    actionTimeout: 20_000,
    navigationTimeout: 120_000,
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    video: 'off',
  },
})
