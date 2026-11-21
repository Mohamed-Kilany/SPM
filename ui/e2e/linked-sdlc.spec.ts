import { expect, test, type APIRequestContext, type Locator, type Page } from '@playwright/test'
import AxeBuilder from '@axe-core/playwright'
import fs from 'node:fs'
import path from 'node:path'

const RUN = Date.now().toString(36)
const PROJECT_NAME = `E2E SDLC ${RUN}`
const PRD_TITLE = `Traceable checkout requirement ${RUN}`
const EPIC_TITLE = `Checkout delivery epic ${RUN}`
const FEATURE_TITLE = `One-tap checkout ${RUN}`
const STORY_TITLE = `Checkout as a returning customer ${RUN}`
const TASK_TITLE = `Implement one-tap payment ${RUN}`
const SUBTASK_TITLE = `Add payment intent contract ${RUN}`
const CASE_TITLE = `Guest checkout with saved card ${RUN}`
const PLAN_NAME = `Release test plan ${RUN}`
const SUITE_NAME = `Checkout smoke ${RUN}`
const RUN_NAME = `Release regression ${RUN}`
const BUG_TITLE = `Saved card checkout returns 500 ${RUN}`
const RELEASE_NAME = `Checkout release ${RUN}`
const RELEASE_VERSION = `1.${new Date().getUTCFullYear() % 100}.0`
const SPRINT_NAME = `Sprint ${RUN}`
function isoDate(offsetDays: number): string {
  const date = new Date()
  date.setUTCDate(date.getUTCDate() + offsetDays)
  return date.toISOString().slice(0, 10)
}
const SPRINT_START = isoDate(0)
const SPRINT_END = isoDate(13)
const BRANCH_NAME = `feature/e2e-${RUN}-checkout`
const ARGO_APP_FALLBACK = `e2e-${RUN}`
const CI_WORKFLOW = `name: ci
on:
  push:
  workflow_dispatch:
defaults:
  run:
    shell: sh
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - run: echo "SPM E2E CI evidence ${RUN}"
`
const DEPLOY_MANIFEST = `apiVersion: v1
kind: ConfigMap
metadata:
  name: spm-e2e-${RUN}
data:
  message: "SPM E2E delivery evidence ${RUN}"
`

type ProjectInfo = {
  id: string
  name: string
  repo_name: string
  app_name: string
}

type WorkspaceBuild = {
  id: string
  commit_sha: string
  branch: string
  status: string
  number: string
}

type WorkspacePullRequest = {
  number: number
  merge_commit_sha: string
}

type WorkspaceDeployment = {
  argo_app: string
  status: string
  health_status: string
}

type WorkspaceSummary = {
  pull_requests: WorkspacePullRequest[]
  builds: WorkspaceBuild[]
  deployments: WorkspaceDeployment[]
}

function envValue(key: string, fallback = ''): string {
  if (process.env[key]) return process.env[key]!
  const file = path.resolve(process.cwd(), '..', '.env')
  if (!fs.existsSync(file)) return fallback
  for (const line of fs.readFileSync(file, 'utf8').split(/\r?\n/)) {
    const match = line.match(/^\s*([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.*)\s*$/)
    if (match?.[1] === key) return (match[2] ?? '').replace(/^['"]|['"]$/g, '')
  }
  return fallback
}

async function selectByText(select: Locator, text: string): Promise<void> {
  const option = select.locator('option').filter({ hasText: text }).first()
  await expect(option).toHaveCount(1)
  const value = await option.getAttribute('value')
  if (!value) throw new Error(`No option value found for ${text}`)
  await select.selectOption(value)
}

async function openTab(page: Page, tab: string): Promise<void> {
  await page.getByTestId(`workspace-tab-${tab}`).click()
  await expect(page.getByTestId(`workspace-tab-${tab}`)).toBeVisible()
}

let cachedAdminToken = ''

async function adminToken(request: APIRequestContext): Promise<string> {
  if (cachedAdminToken) return cachedAdminToken
  const login = await request.post('/api/v1/auth/login', {
    data: { username: 'admin', password: 'Admin@1234' },
  })
  expect(login.ok(), await login.text()).toBeTruthy()
  cachedAdminToken = ((await login.json()) as { token: string }).token
  return cachedAdminToken
}

async function projectDetails(
  request: APIRequestContext,
  projectName: string,
): Promise<ProjectInfo> {
  const token = await adminToken(request)
  const response = await request.get('/api/v1/projects', {
    headers: { Authorization: `Bearer ${token}` },
  })
  expect(response.ok(), await response.text()).toBeTruthy()
  const projects = (await response.json()) as ProjectInfo[]
  const project = projects.find((entry) => entry.name === projectName)
  if (!project?.repo_name) throw new Error(`Project ${projectName} has no provisioned repository`)
  return project
}

async function getWorkspace(
  request: APIRequestContext,
  projectId: string,
): Promise<WorkspaceSummary> {
  const token = await adminToken(request)
  const response = await request.get(`/api/v1/projects/${encodeURIComponent(projectId)}/workspace`, {
    headers: { Authorization: `Bearer ${token}` },
  })
  expect(response.ok(), await response.text()).toBeTruthy()
  return (await response.json()) as WorkspaceSummary
}

async function waitForGiteaRepo(request: APIRequestContext, repoName: string): Promise<void> {
  const root = envValue('GITEA_ROOT_URL', 'http://localhost:3000')
  const token = envValue('GITEA_API_TOKEN')
  const org = envValue('GITEA_ORG', 'spm')
  expect(token, 'GITEA_API_TOKEN must be configured for the E2E repository').not.toBe('')
  const url = `${root}/api/v1/repos/${encodeURIComponent(org)}/${encodeURIComponent(repoName)}`
  await expect
    .poll(
      async () => {
        const response = await request.get(url, { headers: { Authorization: `token ${token}` } })
        return response.status()
      },
      { timeout: 120_000, intervals: [1000, 2000, 3000] },
    )
    .toBe(200)
}

async function putGiteaFile(
  request: APIRequestContext,
  repoName: string,
  filePath: string,
  content: string,
  message: string,
  branch = 'main',
): Promise<void> {
  const root = envValue('GITEA_ROOT_URL', 'http://localhost:3000')
  const token = envValue('GITEA_API_TOKEN')
  const org = envValue('GITEA_ORG', 'spm')
  expect(token, 'GITEA_API_TOKEN must be configured for the E2E commit').not.toBe('')
  const encodedPath = filePath.split('/').map((part) => encodeURIComponent(part)).join('/')
  const url = `${root}/api/v1/repos/${encodeURIComponent(org)}/${encodeURIComponent(repoName)}/contents/${encodedPath}`
  const headers = { Authorization: `token ${token}` }
  let sha = ''
  const existing = await request.get(`${url}?ref=${encodeURIComponent(branch)}`, { headers })
  if (existing.ok()) {
    const payload = (await existing.json()) as { sha?: string }
    sha = payload.sha ?? ''
  }
  const data: Record<string, string> = {
    content: Buffer.from(content).toString('base64'),
    message,
    branch,
  }
  if (sha) data.sha = sha
  const response = sha
    ? await request.put(url, { headers, data })
    : await request.post(url, { headers, data })
  expect(response.ok(), await response.text()).toBeTruthy()
}

async function createArgoApplication(
  request: APIRequestContext,
  appName: string,
  repoName: string,
  namespace: string,
): Promise<void> {
  const argoUrl = envValue('E2E_ARGOCD_URL', 'http://localhost:9090')
  const org = envValue('GITEA_ORG', 'spm')
  const app = {
    apiVersion: 'argoproj.io/v1alpha1',
    kind: 'Application',
    metadata: {
      name: appName,
      namespace: 'argocd',
      labels: { 'spm.io/environment': 'production' },
    },
    spec: {
      project: 'default',
      source: {
        repoURL: `http://gitea:3000/${encodeURIComponent(org)}/${encodeURIComponent(repoName)}.git`,
        targetRevision: 'main',
        path: 'deploy',
      },
      destination: {
        server: 'https://kubernetes.default.svc',
        namespace,
      },
      syncPolicy: {
        automated: { prune: true, selfHeal: true },
        syncOptions: ['CreateNamespace=true'],
      },
    },
  }
  const response = await request.post(`${argoUrl}/api/v1/applications?upsert=true`, { data: app })
  expect(response.ok(), await response.text()).toBeTruthy()
}

async function waitForArgoHealthy(request: APIRequestContext, appName: string): Promise<void> {
  const argoUrl = envValue('E2E_ARGOCD_URL', 'http://localhost:9090')
  await expect
    .poll(
      async () => {
        const response = await request.get(
          `${argoUrl}/api/v1/applications/${encodeURIComponent(appName)}`,
        )
        if (!response.ok()) return `HTTP ${response.status()}`
        const app = (await response.json()) as {
          status?: { sync?: { status?: string }; health?: { status?: string } }
        }
        return `${app.status?.sync?.status ?? ''}/${app.status?.health?.status ?? ''}`
      },
      { timeout: 150_000, intervals: [2000, 3000, 5000] },
    )
    .toBe('Synced/Healthy')
}

async function waitForDeployment(
  request: APIRequestContext,
  projectId: string,
  appName: string,
): Promise<void> {
  await expect
    .poll(
      async () => {
        const workspace = await getWorkspace(request, projectId)
        const deployment = workspace.deployments.find((entry) => entry.argo_app === appName)
        return deployment ? `${deployment.status}/${deployment.health_status}` : ''
      },
      { timeout: 150_000, intervals: [2000, 3000, 5000] },
    )
    .toBe('healthy/healthy')
}

async function expectAccessible(page: Page, label: string): Promise<void> {
  // Toasts fade with opacity; scanning mid-transition produces false contrast
  // failures. Wait until every toast has auto-dismissed.
  await expect(page.getByRole('button', { name: 'Dismiss notification' })).toHaveCount(0, {
    timeout: 9_000,
  })
  await page.waitForTimeout(100)
  const results = await new AxeBuilder({ page })
    .withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa'])
    .analyze()
  const summary = results.violations.map((violation) => ({
    id: violation.id,
    impact: violation.impact,
    help: violation.help,
    nodes: violation.nodes.map((node) => ({
      target: node.target,
      html: node.html,
      failureSummary: node.failureSummary,
    })),
  }))
  expect(summary, `${label} accessibility violations`).toEqual([])
}

test('linked SDLC/STLC flow from story to Argo CD health', async ({ page, request }) => {
  test.setTimeout(600_000)
  const browserErrors: string[] = []
  const externalRequests: string[] = []
  page.on('pageerror', (error) => browserErrors.push(error.message))
  page.on('console', (message) => {
    if (message.type() === 'error') browserErrors.push(message.text())
  })
  page.on('request', (request) => {
    const url = new URL(request.url())
    if (!['http:', 'https:'].includes(url.protocol)) return
    if (!['localhost', '127.0.0.1'].includes(url.hostname)) externalRequests.push(request.url())
  })
  // Lifecycle actions such as unfreeze and cancel ask for confirmation.
  page.on('dialog', (dialog) => dialog.accept())

  // -----------------------------------------------------------------------
  // Sign in and provision a clean project through the UI.
  // -----------------------------------------------------------------------
  await page.goto('/login')
  await page.locator('input[autocomplete="username"]').fill('admin')
  await page.locator('input[autocomplete="current-password"]').fill('Admin@1234')
  await page.getByRole('button', { name: /^Sign in$/ }).click()
  await expect(page).toHaveURL(/\/projects$/)
  await expect(page.getByRole('heading', { name: 'Projects', exact: true })).toBeVisible()
  await expectAccessible(page, 'projects')

  await page.getByRole('button', { name: 'New project' }).click()
  const projectDialog = page.getByRole('dialog', { name: 'New project' })
  await projectDialog.getByPlaceholder('e.g. Atlas Commerce').fill(PROJECT_NAME)
  await projectDialog.getByPlaceholder('What is this project delivering?').fill(
    'UI-created SDLC and STLC chain verified from requirement to Argo CD health.',
  )
  await projectDialog.getByRole('button', { name: 'Create project' }).click()
  await expect(page).toHaveURL(/\/projects\/[0-9a-f-]+$/, { timeout: 120_000 })
  await expect(page.getByRole('heading', { name: PROJECT_NAME, exact: true })).toBeVisible({
    timeout: 120_000,
  })
  await expect(page.getByTestId('workspace-tab-overview')).toBeVisible()
  const project = await projectDetails(request, PROJECT_NAME)
  const repoName = project.repo_name
  const appName = project.app_name || ARGO_APP_FALLBACK
  await waitForGiteaRepo(request, repoName)
  await putGiteaFile(
    request,
    repoName,
    '.gitea/workflows/ci.yml',
    CI_WORKFLOW,
    `ci: add SPM E2E workflow ${RUN}`,
  )
  await putGiteaFile(
    request,
    repoName,
    'deploy/configmap.yaml',
    DEPLOY_MANIFEST,
    `deploy: add Argo CD demo manifest ${RUN}`,
  )
  await expect(page.getByRole('button', { name: /refresh/i })).toHaveCount(0)
  await expect(page.getByText('Live', { exact: true })).toHaveCount(0)
  await expectAccessible(page, 'workspace overview')

  // -----------------------------------------------------------------------
  // Planning: PRD -> epic -> feature.
  // -----------------------------------------------------------------------
  await openTab(page, 'planning')
  await page.getByRole('button', { name: /New PRD/ }).click()
  const prdDialog = page.getByRole('dialog', { name: 'New product requirement' })
  await prdDialog.locator('#prd-title').fill(PRD_TITLE)
  await prdDialog.locator('#prd-summary').fill('Returning customers complete checkout without re-entering card data.')
  await prdDialog.locator('#prd-content').fill(
    'Problem: returning customers abandon checkout. Scope: saved-card payment and receipt. Acceptance: one-tap checkout succeeds and is traceable to production health.',
  )
  await prdDialog.getByRole('button', { name: 'Create PRD' }).click()
  await expect(prdDialog).toBeHidden()
  await expect(page.getByText(PRD_TITLE, { exact: true }).first()).toBeVisible()

  await page.getByRole('button', { name: /New epic/ }).click()
  const epicDialog = page.getByRole('dialog', { name: 'New epic' })
  await epicDialog.locator('#epic-title').fill(EPIC_TITLE)
  await selectByText(epicDialog.locator('#epic-prd'), PRD_TITLE)
  await epicDialog.locator('#epic-description').fill('Deliver a frictionless saved-card checkout outcome.')
  await epicDialog.getByRole('button', { name: 'Create epic' }).click()
  await expect(epicDialog).toBeHidden()
  await expect(page.getByText(EPIC_TITLE, { exact: true }).first()).toBeVisible()

  await page.getByRole('button', { name: /New feature/ }).click()
  const featureDialog = page.getByRole('dialog', { name: 'New feature' })
  await featureDialog.locator('#feature-title').fill(FEATURE_TITLE)
  await selectByText(featureDialog.locator('#feature-epic'), EPIC_TITLE)
  await featureDialog.locator('#feature-description').fill('Customers pay with a saved card in a single interaction.')
  await featureDialog.locator('#feature-ac').fill('Given a saved card, when the customer confirms, then payment succeeds and a receipt is shown.')
  await featureDialog.locator('#feature-estimate').fill('8')
  await featureDialog.getByRole('button', { name: 'Create feature' }).click()
  await expect(featureDialog).toBeHidden()
  await expect(page.getByText(FEATURE_TITLE, { exact: true }).first()).toBeVisible()
  await expectAccessible(page, 'planning')

  // -----------------------------------------------------------------------
  // Board: story -> task -> subtask.
  // -----------------------------------------------------------------------
  await openTab(page, 'board')
  await page.getByRole('button', { name: /New work item/ }).click()
  const storyDialog = page.getByRole('dialog', { name: 'New work item' })
  await storyDialog.locator('#item-type').selectOption('story')
  await storyDialog.locator('#item-title').fill(STORY_TITLE)
  await selectByText(storyDialog.locator('#item-feature'), FEATURE_TITLE)
  await storyDialog.locator('#item-description').fill('Returning customer completes checkout with a saved card.')
  await storyDialog.getByRole('button', { name: 'Create work item' }).click()
  await expect(storyDialog).toBeHidden()
  await expect(page.getByText(STORY_TITLE, { exact: true }).first()).toBeVisible()

  await page.getByRole('button', { name: /New work item/ }).click()
  const taskDialog = page.getByRole('dialog', { name: 'New work item' })
  await taskDialog.locator('#item-type').selectOption('task')
  await taskDialog.locator('#item-title').fill(TASK_TITLE)
  await selectByText(taskDialog.locator('#item-feature'), FEATURE_TITLE)
  await selectByText(taskDialog.locator('#item-parent'), STORY_TITLE)
  await taskDialog.getByRole('button', { name: 'Create work item' }).click()
  await expect(taskDialog).toBeHidden()
  await expect(page.getByText(TASK_TITLE, { exact: true }).first()).toBeVisible()

  await page.getByRole('button', { name: /New work item/ }).click()
  const subtaskDialog = page.getByRole('dialog', { name: 'New work item' })
  await subtaskDialog.locator('#item-type').selectOption('subtask')
  await subtaskDialog.locator('#item-title').fill(SUBTASK_TITLE)
  await selectByText(subtaskDialog.locator('#item-feature'), FEATURE_TITLE)
  await selectByText(subtaskDialog.locator('#item-parent'), TASK_TITLE)
  await subtaskDialog.getByRole('button', { name: 'Create work item' }).click()
  await expect(subtaskDialog).toBeHidden()
  await expect(page.getByText(SUBTASK_TITLE, { exact: true }).first()).toBeVisible()
  await expectAccessible(page, 'board')

  // -----------------------------------------------------------------------
  // Test lab: case -> ordered steps -> run -> failed step -> linked bug.
  // -----------------------------------------------------------------------
  await openTab(page, 'tests')
  await page.getByRole('button', { name: /New test case/ }).click()
  const caseDialog = page.getByRole('dialog', { name: 'New test case' })
  await caseDialog.locator('#case-title').fill(CASE_TITLE)
  await selectByText(caseDialog.locator('#case-feature'), FEATURE_TITLE)
  await caseDialog.locator('#case-description').fill('Verify a saved card completes checkout without re-entry.')
  await caseDialog.locator('#case-preconditions').fill('Signed in customer with one saved card and a non-empty cart.')
  await caseDialog.locator('#case-automation').fill('e2e/traceability.spec.ts')
  await caseDialog.getByPlaceholder('Action: what does the tester do?').fill('Open checkout with a saved card selected.')
  await caseDialog.getByPlaceholder('Expected: what should happen?').fill('The saved card is selected and the payment action is available.')
  await caseDialog.getByRole('button', { name: 'Add step' }).click()
  await caseDialog.getByPlaceholder('Action: what does the tester do?').nth(1).fill('Confirm the one-tap payment.')
  await caseDialog.getByPlaceholder('Expected: what should happen?').nth(1).fill('Payment succeeds, the order is created and a receipt is shown.')
  await caseDialog.getByRole('button', { name: 'Create test case' }).click()
  await expect(caseDialog).toBeHidden()
  await expect(page.getByText(CASE_TITLE, { exact: true }).first()).toBeVisible()
  await expectAccessible(page, 'test lab')

  // A plan schedules the cycle, a suite keeps an ordered selection and the run
  // snapshots both names plus each case revision.
  await page.getByRole('button', { name: /New test plan/ }).click()
  const planDialog = page.getByRole('dialog', { name: 'New test plan' })
  await planDialog.locator('#plan-name').fill(PLAN_NAME)
  await planDialog.locator('#plan-description').fill('Checkout regression cycle for the release candidate.')
  await planDialog.getByRole('button', { name: 'Create test plan' }).click()
  await expect(planDialog).toBeHidden()
  const planBoard = page.getByTestId('test-plan-board')
  await expect(planBoard).toContainText(PLAN_NAME)

  await planBoard.getByRole('button', { name: /New suite/ }).first().click()
  const suiteDialog = page.getByRole('dialog', { name: 'New test suite' })
  await suiteDialog.locator('#suite-name').fill(SUITE_NAME)
  await suiteDialog.getByRole('button', { name: 'Create test suite' }).click()
  await expect(suiteDialog).toBeHidden()
  const suiteRow = page.getByTestId('suite-row').filter({ hasText: SUITE_NAME }).first()
  await expect(suiteRow).toBeVisible()

  await suiteRow.getByRole('button', { name: 'Add cases' }).click()
  const addCasesDialog = page.getByRole('dialog', { name: 'Add cases to suite' })
  await addCasesDialog.locator('#add-cases-mode').selectOption('csv')
  await addCasesDialog.locator('#add-cases-csv').fill(CASE_TITLE)
  await addCasesDialog.getByRole('button', { name: 'Add cases' }).click()
  await expect(addCasesDialog).toBeHidden()
  await expect(page.getByTestId('suite-case').filter({ hasText: CASE_TITLE })).toBeVisible()

  await suiteRow.getByRole('button', { name: 'Run suite' }).click()
  const runDialog = page.getByRole('dialog', { name: 'New test run' })
  await expect(runDialog.locator('#run-scope')).toHaveValue('suite')
  await runDialog.locator('#run-name').fill(RUN_NAME)
  await runDialog.locator('#run-env').selectOption({ label: 'Staging' })
  await runDialog.getByRole('button', { name: 'Start test run' }).click()
  await expect(runDialog).toBeHidden()

  const detail = page.getByRole('dialog', { name: RUN_NAME })
  await expect(detail).toBeVisible()
  await expect(detail).toContainText(PLAN_NAME)
  await expect(detail).toContainText(SUITE_NAME)
  const firstStep = detail.getByTestId('test-step-result').first()
  await firstStep.getByRole('button', { name: 'Failed', exact: true }).click()
  await expect(firstStep.getByRole('button', { name: /Raise bug/ })).toBeVisible()
  await firstStep.getByRole('button', { name: /Raise bug/ }).click()

  const bugDialog = page.getByRole('dialog', { name: 'Raise a linked defect' })
  await bugDialog.locator('#bug-title').fill(BUG_TITLE)
  await bugDialog.locator('#bug-description').fill('The saved-card payment endpoint returned an unexpected server error.')
  await bugDialog.locator('#bug-severity').selectOption('critical')
  await bugDialog.locator('#bug-priority').selectOption('critical')
  await bugDialog.locator('#bug-actual').fill('HTTP 500 from the payment intent endpoint.')
  await bugDialog.getByRole('button', { name: 'Create linked bug' }).click()
  await expect(bugDialog).toBeHidden()
  await expect(detail.getByText('Bug linked')).toBeVisible()
  await detail.getByRole('button', { name: 'Close', exact: true }).click()
  await expect(detail).toBeHidden()

  await openTab(page, 'board')
  await expect(page.getByText(BUG_TITLE, { exact: true }).first()).toBeVisible()

  // -----------------------------------------------------------------------
  // Delivery: release -> branch -> CI -> PR -> merge -> build.
  // -----------------------------------------------------------------------
  await openTab(page, 'delivery')
  await page.getByRole('button', { name: 'Release', exact: true }).first().click()
  const releaseDialog = page.getByRole('dialog', { name: 'Plan release' })
  await releaseDialog.locator('#release-name').fill(RELEASE_NAME)
  await releaseDialog.locator('#release-version').fill(RELEASE_VERSION)
  await releaseDialog.locator('#release-milestone').fill(isoDate(7))
  await releaseDialog.getByRole('button', { name: 'Plan release' }).click()
  await expect(releaseDialog).toBeHidden()
  await expect(page.getByText(RELEASE_NAME, { exact: true }).first()).toBeVisible()

  // Releases start planned. The state changes, freeze, exceptions and sign-off
  // all run from the release card and never from a free-form status select.
  const releaseRow = page.getByTestId('release-row').filter({ hasText: RELEASE_NAME }).first()
  await expect(releaseRow).toContainText('Planned')
  await releaseRow.getByRole('button', { name: 'Start progress', exact: true }).click()
  await expect(releaseRow).toContainText('In progress')

  await releaseRow.getByRole('button', { name: 'Freeze', exact: true }).click()
  const freezeDialog = page.getByRole('dialog', { name: 'Freeze release scope' })
  await freezeDialog.locator('#freeze-reason').fill('Release candidate verification')
  await freezeDialog.getByRole('button', { name: 'Freeze release' }).click()
  await expect(freezeDialog).toBeHidden()
  await expect(releaseRow).toContainText('Frozen')
  await expect(releaseRow).toContainText('Release candidate verification')

  await releaseRow.getByRole('button', { name: 'Exceptions', exact: true }).click()
  const exceptionsDialog = page.getByRole('dialog', { name: 'Release scope exceptions' })
  await exceptionsDialog.locator('#exception-type').selectOption('feature')
  await exceptionsDialog.locator('#exception-reason').fill('Attach delivery feature to the release candidate')
  await exceptionsDialog.getByRole('button', { name: 'Add exception' }).click()
  await expect(exceptionsDialog.getByText('Attach delivery feature to the release candidate')).toBeVisible()
  await exceptionsDialog.getByRole('button', { name: 'Close', exact: true }).click()
  await expect(exceptionsDialog).toBeHidden()

  await releaseRow.getByRole('button', { name: 'Unfreeze', exact: true }).click()
  await expect(releaseRow).not.toContainText('Frozen')

  await page.getByRole('button', { name: 'Branch', exact: true }).first().click()
  const branchDialog = page.getByRole('dialog', { name: 'Link branch' })
  await selectByText(branchDialog.locator('#new-branch-item'), TASK_TITLE)
  await branchDialog.locator('#new-branch-name').fill(BRANCH_NAME)
  await branchDialog.getByRole('button', { name: 'Link branch' }).click()
  await expect(branchDialog).toBeHidden()
  await expect(page.getByText(BRANCH_NAME, { exact: true }).first()).toBeVisible()
  await putGiteaFile(
    request,
    repoName,
    `.spm/${RUN}-branch.md`,
    `# SPM E2E evidence\n\nbranch ${RUN}\n`,
    `test: add branch evidence ${RUN}`,
    BRANCH_NAME,
  )
  const branchBuild = page.getByTestId('build-row').filter({ hasText: BRANCH_NAME }).first()
  await expect(branchBuild).toContainText('Passed', { timeout: 150_000 })

  await page.getByRole('button', { name: 'PR', exact: true }).first().click()
  const prDialog = page.getByRole('dialog', { name: 'Open pull request' })
  await prDialog.locator('#pr-title').fill(`Implement ${FEATURE_TITLE}`)
  await prDialog.locator('#pr-description').fill('Links the checkout implementation to its story, task and CI evidence.')
  await selectByText(prDialog.locator('#pr-item'), TASK_TITLE)
  await selectByText(prDialog.locator('#pr-source'), BRANCH_NAME)
  await prDialog.getByRole('button', { name: 'Open PR' }).click()
  await expect(prDialog).toBeHidden()
  const prLabel = page.getByText(new RegExp(`#\\d+ Implement ${FEATURE_TITLE}`)).first()
  await expect(prLabel).toBeVisible()
  const prNumber = (await prLabel.textContent())?.match(/#(\d+)/)?.[1] ?? '1'

  await page.getByRole('button', { name: 'Merge PR' }).click()
  const mergeDialog = page.getByRole('dialog', { name: 'Merge pull request' })
  await mergeDialog.getByRole('button', { name: 'Merge in Gitea' }).click()
  await expect(mergeDialog).toBeHidden({ timeout: 60_000 })
  await expect(page.getByText('Merged', { exact: true }).first()).toBeVisible({ timeout: 60_000 })

  let mergeSHA = ''
  await expect
    .poll(
      async () => {
        const workspace = await getWorkspace(request, project.id)
        const pullRequest = workspace.pull_requests.find(
          (entry) => entry.number === Number(prNumber),
        )
        mergeSHA = pullRequest?.merge_commit_sha ?? ''
        return mergeSHA
      },
      { timeout: 90_000, intervals: [2000, 3000, 5000] },
    )
    .not.toBe('')

  let mergeBuild: WorkspaceBuild | undefined
  await expect
    .poll(
      async () => {
        const workspace = await getWorkspace(request, project.id)
        mergeBuild = workspace.builds.find((build) => build.commit_sha === mergeSHA)
        return mergeBuild?.status ?? ''
      },
      { timeout: 150_000, intervals: [2000, 3000, 5000] },
    )
    .toBe('success')
  await expect(
    page.getByTestId('build-row').filter({ hasText: `Run #${mergeBuild?.number}` }).first(),
  ).toContainText('Passed', { timeout: 30_000 })

  // Attach the feature to the release so release readiness is evidence-based.
  await openTab(page, 'planning')
  await page.getByRole('button', { name: FEATURE_TITLE, exact: true }).click()
  const editFeatureDialog = page.getByRole('dialog', { name: 'Edit feature' })
  await selectByText(editFeatureDialog.locator('#feature-release'), RELEASE_NAME)
  await editFeatureDialog.getByRole('button', { name: 'Save feature' }).click()
  await expect(editFeatureDialog).toBeHidden()

  // -----------------------------------------------------------------------
  // Deployment: real Argo CD Application -> read-only SPM projection.
  // -----------------------------------------------------------------------
  await openTab(page, 'delivery')
  await createArgoApplication(request, appName, repoName, `spm-${RUN}`)
  await waitForArgoHealthy(request, appName)
  await waitForDeployment(request, project.id, appName)

  // Reload once so the browser reads the reconciled deployment projection even
  // if an SSE reconnect happened while Argo CD was rolling out.
  await page.reload()
  await expect(page.getByTestId('workspace-tab-delivery')).toBeVisible({ timeout: 120_000 })
  await openTab(page, 'delivery')

  const deploymentRow = page.getByTestId('deployment-row').filter({ hasText: appName }).first()
  await expect(deploymentRow).toBeVisible({ timeout: 150_000 })
  await expect(deploymentRow).toContainText('Production')
  await expect(deploymentRow).toContainText('Healthy')
  await expectAccessible(page, 'delivery')

  // -----------------------------------------------------------------------
  // Sprint and report import close the planning/test reporting loop.
  // -----------------------------------------------------------------------
  await openTab(page, 'reports')
  await expect(page.getByRole('heading', { name: 'Reports & release control' })).toBeVisible()
  await page.getByRole('button', { name: 'Sprint', exact: true }).click()
  const sprintDialog = page.getByRole('dialog', { name: 'Plan a sprint' })
  await sprintDialog.locator('#sprint-name').fill(SPRINT_NAME)
  await sprintDialog.locator('#sprint-goal').fill('Ship traceable saved-card checkout.')
  await sprintDialog.locator('#sprint-capacity').fill('24')
  await sprintDialog.locator('#sprint-start').fill(SPRINT_START)
  await sprintDialog.locator('#sprint-end').fill(SPRINT_END)
  await sprintDialog.getByRole('button', { name: 'Create sprint' }).click()
  await expect(sprintDialog).toBeHidden()

  const sprintCard = page.getByTestId('sprint-row').filter({ hasText: SPRINT_NAME }).first()
  await expect(sprintCard).toBeVisible()
  await expect(sprintCard).toContainText('Planned')
  await sprintCard.getByRole('button', { name: 'Start', exact: true }).click()
  const startDialog = page.getByRole('dialog', { name: 'Start sprint' })
  await startDialog.getByRole('button', { name: 'Start sprint' }).click()
  await expect(startDialog).toBeHidden()
  await expect(sprintCard).toContainText('Active')

  await openTab(page, 'board')
  await page.getByText(TASK_TITLE, { exact: true }).first().click()
  const taskEditDialog = page.getByRole('dialog', { name: /New work item|Edit work item|.*Task/ })
  await taskEditDialog.locator('#item-sprint').selectOption({ label: SPRINT_NAME })
  await taskEditDialog.getByRole('button', { name: 'Save work item' }).click()
  await expect(taskEditDialog).toBeHidden()

  await openTab(page, 'reports')
  await expect(sprintCard).toContainText('Active')
  await expect(sprintCard.getByText(/0\/1 items/)).toBeVisible()

  await sprintCard.getByRole('button', { name: 'Metrics', exact: true }).click()
  const metricsDialog = page.getByRole('dialog', { name: `Sprint metrics · ${SPRINT_NAME}` })
  await expect(metricsDialog.getByText('Committed', { exact: true })).toBeVisible()
  await expect(metricsDialog.getByText('Completed', { exact: true })).toBeVisible()
  await expect(metricsDialog.getByText('Velocity', { exact: true })).toBeVisible()
  await metricsDialog.getByRole('button', { name: 'Close', exact: true }).click()
  await expect(metricsDialog).toBeHidden()

  await sprintCard.getByRole('button', { name: 'Close', exact: true }).click()
  const closeDialog = page.getByRole('dialog', { name: 'Close sprint' })
  await expect(closeDialog).toContainText('1 incomplete')
  await expect(closeDialog).toContainText(TASK_TITLE)
  await closeDialog.getByRole('button', { name: 'Close sprint' }).click()
  await expect(closeDialog).toBeHidden()
  await expect(sprintCard).toContainText('Completed')
  await expect(sprintCard).toContainText('1 carried over')

  await page.getByRole('button', { name: 'Import results' }).click()
  const importDialog = page.getByRole('dialog', { name: 'Import automated test results' })
  await importDialog.locator('#import-environment').selectOption({ label: 'Staging' })
  await selectByText(importDialog.locator('#import-feature'), FEATURE_TITLE)
  const junit = `<?xml version="1.0" encoding="UTF-8"?>
<testsuites>
  <testsuite name="e2e" tests="2">
    <testcase classname="e2e.traceability" name="${CASE_TITLE}" time="0.12" />
    <testcase classname="e2e.health" name="Automated health smoke ${RUN}" time="0.08" />
  </testsuite>
</testsuites>`
  await importDialog.locator('#import-file').setInputFiles({
    name: 'junit-e2e.xml',
    mimeType: 'application/xml',
    buffer: Buffer.from(junit),
  })
  await importDialog.getByRole('button', { name: 'Import results' }).click()
  await expect(importDialog.getByText('2 result(s): 1 matched, 1 created, 0 unmatched.')).toBeVisible()
  await importDialog.getByRole('button', { name: 'Close', exact: true }).click()
  await expect(importDialog).toBeHidden()

  // Imported passing results clear the test gate; the open critical defect still
  // blocks release sign-off until it is worked through its bug workflow.
  const readinessButton = page.getByRole('button', { name: new RegExp(RELEASE_NAME) })
  await expect(readinessButton).toContainText('Blocked')
  await expect(page.getByText('2/2 latest test results passed')).toBeVisible()
  await expect(page.getByText('1 open critical bug(s), 1 open in total')).toBeVisible()

  await openTab(page, 'board')
  await page.getByText(BUG_TITLE, { exact: true }).first().click()
  const bugEditDialog = page.getByRole('dialog', { name: /· Bug/ })
  await expect(bugEditDialog).toBeVisible()
  for (const status of ['Triaged', 'In progress', 'Fixed', 'Verified', 'Closed']) {
    await bugEditDialog.getByRole('button', { name: new RegExp(`→ ${status}$`) }).click()
  }
  await expect(bugEditDialog.getByText('Closed', { exact: true }).first()).toBeVisible()
  await bugEditDialog.getByRole('button', { name: 'Cancel', exact: true }).click()
  await expect(bugEditDialog).toBeHidden()

  await openTab(page, 'reports')
  await expect(readinessButton).toContainText('Ready')
  await expect(page.getByText('No open defects in the release scope')).toBeVisible()
  await expect(page.getByText('Delivery evidence', { exact: true })).toBeVisible()
  await expectAccessible(page, 'reports')

  // Sign-off records evidence for the in-progress release, then the release
  // moves to released without a free-form status edit.
  await openTab(page, 'delivery')
  await expect(releaseRow).toContainText('In progress')
  await releaseRow.getByRole('button', { name: 'Sign off', exact: true }).click()
  const signOffDialog = page.getByRole('dialog', { name: 'Release sign-off' })
  await expect(signOffDialog.getByText('Ready', { exact: true })).toBeVisible()
  await signOffDialog
    .locator('#sign-off-evidence')
    .fill('Regression suite green; no open critical bugs; release notes approved.')
  await signOffDialog.getByRole('button', { name: 'Record sign-off' }).click()
  await expect(signOffDialog).toBeHidden()
  await expect(releaseRow).toContainText('Signed off')
  await releaseRow.getByRole('button', { name: 'Mark released', exact: true }).click()
  await expect(releaseRow).toContainText('Released')

  const downloadPromise = page.waitForEvent('download')
  await page.getByRole('button', { name: /Coverage CSV/ }).click()
  const download = await downloadPromise
  expect(download.suggestedFilename()).toContain('features')

  // -----------------------------------------------------------------------
  // In-app Code browser: real repository, branches, commits and merged PR.
  // -----------------------------------------------------------------------
  await openTab(page, 'code')
  await expect(page.getByRole('heading', { name: 'Code', exact: true })).toBeVisible()
  await expect(page.getByText(repoName, { exact: true }).first()).toBeVisible()
  await page.getByRole('button', { name: 'deploy', exact: true }).click()
  await expect(page.getByText('configmap.yaml', { exact: true })).toBeVisible()
  await page.getByText('configmap.yaml', { exact: true }).click()
  await expect(page.getByText(`SPM E2E delivery evidence ${RUN}`)).toBeVisible()
  await page.getByRole('button', { name: /^Branches/ }).click()
  await expect(page.getByText(BRANCH_NAME, { exact: true })).toBeVisible()
  await page.getByRole('button', { name: /^Commits/ }).click()
  await expect(page.getByText(`deploy: add Argo CD demo manifest ${RUN}`)).toBeVisible()
  await page.getByRole('button', { name: /^Pull requests/ }).click()
  await expect(page.getByText(new RegExp(`#\\d+ Implement ${FEATURE_TITLE}`))).toBeVisible()
  await expectAccessible(page, 'code')

  // -----------------------------------------------------------------------
  // Traceability: one feature row with no gaps and complete delivery health.
  // -----------------------------------------------------------------------
  await openTab(page, 'traceability')
  await expect(page.getByRole('heading', { name: 'Traceability matrix' })).toBeVisible()
  await expect(page.getByRole('button', { name: /refresh/i })).toHaveCount(0)
  const featureRow = page.getByRole('row').filter({ hasText: FEATURE_TITLE })
  await expect(featureRow).toBeVisible()
  await expect(featureRow).toContainText('1 story')
  await expect(featureRow).toContainText('1 PR')
  await expect(featureRow).toContainText('Chain complete')
  await expect(page.getByText('Every feature has a complete requirement-to-health chain.')).toBeVisible()
  await expect(page.getByText('0 gaps', { exact: true })).toBeVisible()
  await expectAccessible(page, 'traceability')

  expect(browserErrors, `browser errors: ${browserErrors.join('\n')}`).toEqual([])
  expect(externalRequests, `external browser requests: ${externalRequests.join('\n')}`).toEqual([])
})
