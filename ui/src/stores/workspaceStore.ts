import { computed, ref } from 'vue'
import { defineStore } from 'pinia'
import { api } from '../api/client'
import type {
  ActivityEvent,
  Build,
  CustomField,
  CustomFieldValue,
  Deployment,
  Epic,
  Feature,
  GitCommit,
  Member,
  PRD,
  ProjectLabel,
  PullRequest,
  Release,
  SavedFilter,
  Sprint,
  TestCase,
  TestPlan,
  TestRun,
  TestSuite,
  TestSuiteCase,
  WorkItem,
  Workspace,
} from '../types/workspace'

// A small in-memory snapshot cache lets a returning project paint the last
// known board immediately while the authoritative snapshot is fetched. It is
// deliberately module-scoped: resetting the active project must not throw away
// the cache, and a full page reload clears it.
const SNAPSHOT_CACHE_LIMIT = 5
const snapshotCache = new Map<string, Workspace>()

function cacheSnapshot(id: string, snapshot: Workspace): void {
  snapshotCache.delete(id)
  snapshotCache.set(id, snapshot)
  while (snapshotCache.size > SNAPSHOT_CACHE_LIMIT) {
    const oldest = snapshotCache.keys().next().value
    if (!oldest) break
    snapshotCache.delete(oldest)
  }
}

export const useWorkspaceStore = defineStore('workspace', () => {
  const workspace = ref<Workspace | null>(null)
  const loading = ref(false)
  const refreshing = ref(false)
  const error = ref('')
  const projectId = ref('')
  let refreshPromise: Promise<void> | null = null

  const project = computed(() => workspace.value?.project ?? null)
  const metrics = computed(() => workspace.value?.metrics ?? null)
  const prds = computed<PRD[]>(() => workspace.value?.prds ?? [])
  const epics = computed<Epic[]>(() => workspace.value?.epics ?? [])
  const features = computed<Feature[]>(() => workspace.value?.features ?? [])
  const workItems = computed<WorkItem[]>(() => workspace.value?.work_items ?? [])
  const labels = computed<ProjectLabel[]>(() => workspace.value?.labels ?? [])
  const allCustomFields = computed<CustomField[]>(() => workspace.value?.custom_fields ?? [])
  const customFields = computed<CustomField[]>(() =>
    allCustomFields.value.filter((field) => !field.archived_at),
  )
  const customFieldValues = computed<CustomFieldValue[]>(
    () => workspace.value?.custom_field_values ?? [],
  )
  const savedFilters = computed<SavedFilter[]>(() => workspace.value?.saved_filters ?? [])
  const sprints = computed<Sprint[]>(() => workspace.value?.sprints ?? [])
  const releases = computed<Release[]>(() => workspace.value?.releases ?? [])
  const testCases = computed<TestCase[]>(() => workspace.value?.test_cases ?? [])
  const testPlans = computed<TestPlan[]>(() => workspace.value?.test_plans ?? [])
  const testSuites = computed<TestSuite[]>(() => workspace.value?.test_suites ?? [])
  const testSuiteCases = computed<TestSuiteCase[]>(() => workspace.value?.test_suite_cases ?? [])
  const testRuns = computed<TestRun[]>(() => workspace.value?.test_runs ?? [])
  const repositories = computed(() => workspace.value?.repositories ?? [])
  const branches = computed(() => workspace.value?.branches ?? [])
  const commits = computed<GitCommit[]>(() => workspace.value?.commits ?? [])
  const pullRequests = computed<PullRequest[]>(() => workspace.value?.pull_requests ?? [])
  const builds = computed<Build[]>(() => workspace.value?.builds ?? [])
  const deployments = computed<Deployment[]>(() => workspace.value?.deployments ?? [])
  const activity = computed<ActivityEvent[]>(() => workspace.value?.activity ?? [])
  const members = computed<Member[]>(() => workspace.value?.members ?? [])

  async function load(id: string): Promise<void> {
    if (!id) return
    projectId.value = id
    error.value = ''
    const cached = snapshotCache.get(id)
    if (cached) {
      workspace.value = cached
      loading.value = false
    } else {
      loading.value = true
    }
    try {
      const next = await api.getWorkspace(id)
      if (projectId.value !== id) return
      workspace.value = next
      cacheSnapshot(id, next)
    } catch (err) {
      if (projectId.value !== id) return
      error.value = err instanceof Error ? err.message : 'Failed to load workspace'
    } finally {
      if (projectId.value === id) loading.value = false
    }
  }

  async function refresh(): Promise<void> {
    if (!projectId.value) return
    // Concurrent callers share one request. Optimistic board moves rely on this:
    // awaiting refresh must mean the latest snapshot is installed, not that a
    // second caller returned immediately while the first request was in flight.
    if (refreshPromise) return refreshPromise
    const id = projectId.value
    refreshing.value = true
    refreshPromise = (async () => {
      try {
        const next = await api.getWorkspace(id)
        if (projectId.value === id) {
          workspace.value = next
          cacheSnapshot(id, next)
          error.value = ''
        }
      } catch (err) {
        if (projectId.value === id) {
          error.value = err instanceof Error ? err.message : 'Failed to refresh workspace'
        }
      } finally {
        refreshing.value = false
        refreshPromise = null
      }
    })()
    return refreshPromise
  }

  function reset(): void {
    workspace.value = null
    projectId.value = ''
    error.value = ''
    loading.value = false
    refreshing.value = false
    refreshPromise = null
  }

  function clearSnapshotCache(): void {
    snapshotCache.clear()
  }

  function workItemById(id: string): WorkItem | undefined {
    return workItems.value.find((item) => item.id === id)
  }

  function featureById(id: string): Feature | undefined {
    return features.value.find((feature) => feature.id === id)
  }

  function epicById(id: string): Epic | undefined {
    return epics.value.find((epic) => epic.id === id)
  }

  function prdById(id: string): PRD | undefined {
    return prds.value.find((prd) => prd.id === id)
  }

  function environmentById(id: string) {
    return workspace.value?.environments.find((environment) => environment.id === id)
  }

  function labelByName(name: string): ProjectLabel | undefined {
    return labels.value.find((label) => label.name === name)
  }

  function customFieldById(id: string): CustomField | undefined {
    return allCustomFields.value.find((field) => field.id === id)
  }

  function customFieldByKey(key: string): CustomField | undefined {
    return allCustomFields.value.find((field) => field.key === key)
  }

  function customValuesFor(entityId: string, entityType = 'work_item'): CustomFieldValue[] {
    return customFieldValues.value.filter(
      (value) => value.entity_id === entityId && value.entity_type === entityType,
    )
  }

  function savedFiltersFor(scope: string): SavedFilter[] {
    return savedFilters.value.filter((filter) => filter.scope === scope)
  }

  return {
    workspace,
    loading,
    refreshing,
    error,
    projectId,
    project,
    metrics,
    prds,
    epics,
    features,
    workItems,
    labels,
    allCustomFields,
    customFields,
    customFieldValues,
    savedFilters,
    sprints,
    releases,
    testCases,
    testPlans,
    testSuites,
    testSuiteCases,
    testRuns,
    repositories,
    branches,
    commits,
    pullRequests,
    builds,
    deployments,
    activity,
    members,
    load,
    refresh,
    reset,
    clearSnapshotCache,
    workItemById,
    featureById,
    epicById,
    prdById,
    environmentById,
    labelByName,
    customFieldById,
    customFieldByKey,
    customValuesFor,
    savedFiltersFor,
  }
})
