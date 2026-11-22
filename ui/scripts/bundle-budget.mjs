// Enforces the production bundle budget documented in docs/BOARD-PERFORMANCE.md.
// The budget is measured on gzip bytes because that is what the browser
// downloads; raw bytes would punish source maps or readable identifiers.
import fs from 'node:fs'
import path from 'node:path'
import zlib from 'node:zlib'

const root = process.cwd()
const assetsDir = path.join(root, 'dist', 'assets')
const budgetPath = path.join(root, 'performance-budget.json')

function fail(message) {
  console.error(`bundle-budget: ${message}`)
  process.exit(1)
}

if (!fs.existsSync(assetsDir)) fail('dist/assets is missing; run "npm run build" first')
if (!fs.existsSync(budgetPath)) fail('performance-budget.json is missing')

const budget = JSON.parse(fs.readFileSync(budgetPath, 'utf8'))
const files = fs
  .readdirSync(assetsDir)
  .filter((name) => name.endsWith('.js') || name.endsWith('.css'))
  .map((name) => {
    const buffer = fs.readFileSync(path.join(assetsDir, name))
    return {
      name,
      type: name.endsWith('.js') ? 'js' : 'css',
      gzip: zlib.gzipSync(buffer, { level: 9 }).length,
    }
  })

if (!files.length) fail('no JavaScript or CSS assets found in dist/assets')

const total = (type) => files.filter((file) => file.type === type).reduce((sum, file) => sum + file.gzip, 0)
const jsGzip = total('js')
const cssGzip = total('css')
const largest = files.reduce((max, file) => (file.gzip > max.gzip ? file : max), files[0])

const kib = (bytes) => `${(bytes / 1024).toFixed(1)} KiB`
const checks = [
  ['JavaScript (gzip)', jsGzip, budget.jsGzipBytes],
  ['CSS (gzip)', cssGzip, budget.cssGzipBytes],
  [`Largest chunk (${largest.name})`, largest.gzip, budget.largestChunkGzipBytes],
]

console.log('Production bundle budget')
for (const [label, actual, limit] of checks) {
  const status = actual <= limit ? 'ok' : 'OVER'
  console.log(`  ${status.padEnd(4)} ${label}: ${kib(actual)} / ${kib(limit)}`)
}
console.log('  Largest assets:')
for (const file of files.sort((a, b) => b.gzip - a.gzip).slice(0, 5)) {
  console.log(`    ${kib(file.gzip).padStart(10)}  ${file.name}`)
}

const breaches = checks.filter(([, actual, limit]) => actual > limit)
if (breaches.length) {
  for (const [label, actual, limit] of breaches) {
    console.error(`bundle-budget: ${label} is ${kib(actual)}, budget is ${kib(limit)}`)
  }
  process.exit(1)
}
