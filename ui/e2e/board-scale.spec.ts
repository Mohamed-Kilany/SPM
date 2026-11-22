import { expect, test, type Locator, type Page } from '@playwright/test'
import fs from 'node:fs'

// BRD-03 reference dataset: 2,000 cards spread across the seven board columns,
// with the same card types, priorities and optional fields a real project uses.
// The workspace is synthesized so the stress test does not depend on seeded
// project data, and the response is intercepted before the SPA loads.
const RUN = Date.now().toString(36)
const PROJECT_ID = `board-scale-${RUN}`
const PROJECT_NAME = `Board scale ${RUN}`
const CARD_COUNT = 2000
const STATUSES = ['backlog', 'ready', 'in_progress', 'in_review', 'in_qa', 'done', 'blocked']
const TYPES = ['story', 'task', 'bug', 'spike', 'chore', 'subtask']
const PRIORITIES = ['critical', 'high', 'medium', 'low']
const NOW = '2026-01-01T00:00:00Z'

type Counters = {
  workspace: number
  status: number
}

function buildWorkItem(index: number): Record<string, unknown> {
  // Card 1 is always Ready / Task / Critical so the drag and keyboard move
  // scenarios have a deterministic, legal transition. It is the only critical
  // card in Ready, which keeps it inside the first rendered page.
  const isAnchor = index === 1
  return {
    id: `stress-${index}`,
    project_id: PROJECT_ID,
    item_number: index + 1,
    key: `STR-${index + 1}`,
    feature_id: null,
    parent_id: null,
    type: isAnchor ? 'task' : TYPES[index % TYPES.length],
    title: `Scale card ${index + 1}: checkout flow ${index % 17}`,
    description: 'Generated board-scale card.',
    acceptance_criteria: '',
    status: isAnchor ? 'ready' : STATUSES[index % STATUSES.length],
    priority: isAnchor ? 'critical' : PRIORITIES[(index % 3) + 1],
    severity: null,
    assignee_id: index % 4 === 0 ? 'user-1' : null,
    reporter_id: 'user-1',
    estimate_points: (index % 8) + 1,
    sprint_id: null,
    release_id: null,
    environment_id: null,
    due_date: null,
    reproduction_steps: '',
    expected_result: '',
    actual_result: '',
    labels: index % 3 === 0 ? ['scale', 'board'] : [],
    created_at: NOW,
    updated_at: NOW,
    closed_at: null,
    revision: 1,
  }
}

function buildWorkspace(): Record<string, unknown> {
  return {
    project: {
      id: PROJECT_ID,
      name: PROJECT_NAME,
      description: 'Synthetic 2,000-card dataset for the BRD-03 stress test.',
      key_prefix: 'STR',
      status: 'active',
      owner_id: 'user-1',
      repo_name: `board-scale-${RUN}`,
      default_branch: 'main',
      app_name: `board-scale-${RUN}`,
      settings: {},
      created_at: NOW,
      updated_at: NOW,
    },
    members: [],
    prds: [],
    epics: [],
    features: [],
    work_items: Array.from({ length: CARD_COUNT }, (_, index) => buildWorkItem(index)),
    labels: [],
    custom_fields: [],
    custom_field_values: [],
    saved_filters: [],
    sprints: [],
    releases: [],
    environments: [],
    test_cases: [],
    test_plans: [],
    test_suites: [],
    test_suite_cases: [],
    test_shared_steps: [],
    test_runs: [],
    test_results: [],
    test_step_results: [],
    repositories: [],
    branches: [],
    commits: [],
    pull_requests: [],
    builds: [],
    deployments: [],
    health_checks: [],
    links: [],
    activity: [],
    metrics: {
      prd_count: 0,
      epic_count: 0,
      feature_count: 0,
      work_item_count: CARD_COUNT,
      open_bug_count: 0,
      test_case_count: 0,
      test_run_count: 0,
      pass_rate: 0,
      coverage_percent: 0,
      deployment_count: 0,
      healthy_deployments: 0,
      degraded_deployments: 0,
      unlinked_features: 0,
      untested_features: 0,
      flaky_test_count: 0,
      cycle_time_hours: 0,
      delivery_confidence: 0,
      test_trend: [],
    },
    revision: 1,
  }
}

async function signIn(page: Page): Promise<void> {
  await page.goto('/login')
  await page.locator('input[autocomplete="username"]').fill('admin')
  await page.locator('input[autocomplete="current-password"]').fill('Admin@1234')
  await page.getByRole('button', { name: /^Sign in$/ }).click()
  await expect(page).toHaveURL(/\/projects$/)
}

async function mockWorkspace(
  page: Page,
  workspace: Record<string, unknown>,
  counters: Counters,
): Promise<void> {
  await page.route(`**/api/v1/projects/${PROJECT_ID}/workspace`, async (route) => {
    counters.workspace += 1
    await route.fulfill({ json: workspace })
  })
  await page.route(`**/api/v1/projects/${PROJECT_ID}/work-items/*/status`, async (route) => {
    counters.status += 1
    const body = route.request().postDataJSON() as { status?: string }
    await route.fulfill({
      json: { id: 'stress-1', revision: 2, status: body.status ?? 'in_progress' },
    })
  })
  await page.route('**/api/v1/auth/stream-ticket', (route) =>
    route.fulfill({ json: { ticket: `board-scale-${RUN}` } }),
  )
  // An empty SSE response is enough for the page to mark the stream connected.
  // The client reconnects with backoff, which exercises refresh coalescing
  // without depending on a live event source.
  await page.route(`**/api/v1/projects/${PROJECT_ID}/stream*`, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'text/event-stream',
      body: ': connected\n\n',
    }),
  )
}

async function installPerfObservers(page: Page): Promise<void> {
  await page.addInitScript(() => {
    const state = { cls: 0, boardMounted: false }
    ;(window as unknown as { __boardScale?: typeof state }).__boardScale = state
    try {
      new PerformanceObserver((list) => {
        for (const entry of list.getEntries() as Array<
          PerformanceEntry & { hadRecentInput?: boolean; value?: number }
        >) {
          if (!entry.hadRecentInput) state.cls += entry.value ?? 0
        }
      }).observe({ type: 'layout-shift', buffered: true })
    } catch {
      // layout-shift is Chromium-only; the suite runs in Chrome.
    }
    // Reset CLS when the board shell mounts so the budget measures card
    // insertion, not the route-level skeleton.
    const observer = new MutationObserver(() => {
      if (!document.querySelector('[data-testid="board-perf"]')) return
      state.cls = 0
      state.boardMounted = true
      observer.disconnect()
    })
    observer.observe(document.documentElement, { childList: true, subtree: true })
  })
}

async function dragCardToColumn(page: Page, card: Locator, columnKey: string): Promise<void> {
  const column = page.locator(`[data-board-column$=":${columnKey}"]`).first()
  const source = await card.boundingBox()
  const target = await column.boundingBox()
  if (!source || !target) throw new Error(`Cannot drag to column ${columnKey}`)
  const startX = source.x + source.width / 2
  const startY = source.y + source.height / 2
  await page.mouse.move(startX, startY)
  await page.mouse.down()
  // Cross the 6 px pointer threshold, then aim at the target column header so
  // the drop target is unambiguous even when the column is tall.
  await page.mouse.move(startX + 12, startY + 6, { steps: 3 })
  await page.mouse.move(target.x + target.width / 2, target.y + 80, { steps: 12 })
  await page.mouse.up()
}

function median(values: number[]): number {
  if (!values.length) return 0
  const sorted = [...values].sort((a, b) => a - b)
  const middle = Math.floor(sorted.length / 2)
  return sorted.length % 2 === 0
    ? ((sorted[middle - 1] ?? 0) + (sorted[middle] ?? 0)) / 2
    : (sorted[middle] ?? 0)
}

test('2,000-card board meets the BRD-03 performance budget', async ({ page }, testInfo) => {
  test.setTimeout(300_000)
  const counters: Counters = { workspace: 0, status: 0 }
  const workspace = buildWorkspace()

  await installPerfObservers(page)
  await signIn(page)
  await mockWorkspace(page, workspace, counters)

  const cdp = await page.context().newCDPSession(page)

  // -------------------------------------------------------------------------
  // Initial render.
  // -------------------------------------------------------------------------
  const navigationStarted = Date.now()
  await page.goto(`/projects/${PROJECT_ID}?tab=board`, { waitUntil: 'domcontentloaded' })
  await expect(page.getByTestId('board-perf')).toBeAttached()
  await expect
    .poll(async () => Number(await page.getByTestId('board-perf').getAttribute('data-render-ms')))
    .toBeGreaterThan(0)
  await expect(page.locator('[data-board-card]').first()).toBeVisible()
  const renderMs = Number(await page.getByTestId('board-perf').getAttribute('data-render-ms'))
  const navigationMs = Date.now() - navigationStarted

  expect(renderMs, 'initial board render budget').toBeLessThan(1500)

  await expect
    .poll(async () =>
      page.evaluate(
        () =>
          (window as unknown as { __boardScale?: { boardMounted?: boolean } }).__boardScale
            ?.boardMounted ?? false,
      ),
    )
    .toBe(true)
  await page.waitForTimeout(400)
  const initialCls = await page.evaluate(
    () =>
      (window as unknown as { __boardScale?: { cls?: number } }).__boardScale?.cls ?? 0,
  )

  // -------------------------------------------------------------------------
  // Lazy pages: 40 cards per column until the reader asks for more.
  // -------------------------------------------------------------------------
  const readyColumn = page.locator('[data-board-column="all:ready"]')
  await expect(readyColumn.locator('article')).toHaveCount(40)
  await page.getByTestId('board-show-more-all-ready').click()
  await expect(readyColumn.locator('article')).toHaveCount(80)
  await page.waitForTimeout(200)
  const pagedCls = await page.evaluate(
    () =>
      (window as unknown as { __boardScale?: { cls?: number } }).__boardScale?.cls ?? 0,
  )
  const cls = Math.max(initialCls, pagedCls)
  expect(cls, 'layout shift while cards load').toBeLessThan(0.1)

  // -------------------------------------------------------------------------
  // Interaction trace and a pointer drag.
  // -------------------------------------------------------------------------
  const traceEvents: unknown[] = []
  cdp.on('Tracing.dataCollected', (event) => {
    traceEvents.push(...((event.value as unknown[]) ?? []))
  })
  const traceComplete = new Promise<void>((resolve) => {
    cdp.once('Tracing.tracingComplete', () => resolve())
  })
  await cdp.send('Tracing.start', {
    categories: 'devtools.timeline,blink.user_timing,loading',
    transferMode: 'ReportEvents',
  })

  const anchor = page.locator('[data-board-card="stress-1"]')
  await expect(anchor).toBeVisible()
  const statusBefore = counters.status
  await dragCardToColumn(page, anchor, 'in_progress')
  await expect.poll(() => counters.status).toBe(statusBefore + 1)
  await expect
    .poll(async () => Number(await page.getByTestId('board-perf').getAttribute('data-move-ms')))
    .toBeGreaterThan(0)
  const moveMs = Number(await page.getByTestId('board-perf').getAttribute('data-move-ms'))
  expect(moveMs, 'drag interaction budget').toBeLessThan(100)

  // The mocked refresh returns the original snapshot, so the optimistic card
  // returns to Ready once the request settles.
  await expect(page.locator('[data-board-column="all:ready"] [data-board-card="stress-1"]')).toHaveCount(1)

  // -------------------------------------------------------------------------
  // Scrolling: shrink the viewport so the board itself scrolls horizontally.
  // -------------------------------------------------------------------------
  await page.setViewportSize({ width: 1100, height: 900 })
  const scroller = await page.evaluate(() => {
    const element = document.querySelector<HTMLElement>('[data-testid="board-scroll"]')
    return element
      ? { scrollWidth: element.scrollWidth, clientWidth: element.clientWidth }
      : null
  })
  expect(scroller).not.toBeNull()
  expect(scroller!.scrollWidth).toBeGreaterThan(scroller!.clientWidth)

  const scrollFps = await page.evaluate(async () => {
    const element = document.querySelector<HTMLElement>('[data-testid="board-scroll"]')
    if (!element) return 0
    let frames = 0
    let direction = 1
    const started = performance.now()
    await new Promise<void>((resolve) => {
      function step(now: number): void {
        frames += 1
        element!.scrollLeft += direction * 24
        const max = element!.scrollWidth - element!.clientWidth
        if (element!.scrollLeft >= max) direction = -1
        if (element!.scrollLeft <= 0) direction = 1
        if (now - started >= 1500) resolve()
        else requestAnimationFrame(step)
      }
      requestAnimationFrame(step)
    })
    return frames / ((performance.now() - started) / 1000)
  })
  expect(scrollFps, 'board scroll frame rate').toBeGreaterThanOrEqual(50)

  await cdp.send('Tracing.end')
  await traceComplete
  const tracePath = testInfo.outputPath('board-scale-trace.json')
  fs.writeFileSync(tracePath, JSON.stringify({ traceEvents }))
  await testInfo.attach('board-scale-trace', {
    path: tracePath,
    contentType: 'application/json',
  })

  // -------------------------------------------------------------------------
  // Accelerated SSE soak: 60 full snapshot replacements. A leak of one
  // snapshot per cycle would grow the heap by well over 100 MB; the budget is
  // 48 MB after forced GC.
  // -------------------------------------------------------------------------
  await cdp.send('HeapProfiler.enable')
  await cdp.send('HeapProfiler.collectGarbage')
  const heapBefore = ((await cdp.send('Runtime.getHeapUsage')) as { usedSize: number }).usedSize
  const moveTimings: number[] = []
  const cycles = 60

  for (let cycle = 0; cycle < cycles; cycle += 1) {
    const expectedStatus = counters.status + 1
    await page.locator('[data-board-card="stress-1"]').focus()
    await page.keyboard.press('Alt+ArrowRight')
    await expect.poll(() => counters.status, { timeout: 10_000 }).toBe(expectedStatus)
    await expect(
      page.locator('[data-board-column="all:ready"] [data-board-card="stress-1"]'),
      'refresh returns the card to its authoritative column',
    ).toHaveCount(1)
    const timing = Number(await page.getByTestId('board-perf').getAttribute('data-move-ms'))
    if (timing > 0) moveTimings.push(timing)
  }

  await cdp.send('HeapProfiler.collectGarbage')
  const heapAfter = ((await cdp.send('Runtime.getHeapUsage')) as { usedSize: number }).usedSize
  const heapGrowth = heapAfter - heapBefore
  expect(heapGrowth, 'heap growth across accelerated SSE refreshes').toBeLessThan(48 * 1024 * 1024)

  const medianMoveMs = median(moveTimings)
  expect(medianMoveMs, 'median keyboard move budget').toBeLessThan(100)

  await testInfo.attach('board-scale-metrics', {
    body: JSON.stringify(
      {
        cardCount: CARD_COUNT,
        renderMs,
        navigationMs,
        moveMs,
        medianMoveMs,
        scrollFps,
        cls,
        refreshCycles: cycles,
        heapBefore,
        heapAfter,
        heapGrowth,
      },
      null,
      2,
    ),
    contentType: 'application/json',
  })
})
