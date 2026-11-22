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

export const useWorkspaceStore = defineStore('workspace', () => {
  const workspace = ref<Workspace | null>(null)
  const loading = ref(false)
  const refreshing = ref(false)
  const error = ref('')
  const projectId = ref('')

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
    loading.value = true
    error.value = ''
    try {
      workspace.value = await api.getWorkspace(id)
    } catch (err) {
      error.value = err instanceof Error ? err.message : 'Failed to load workspace'
    } finally {
      loading.value = false
    }
  }

  async function refresh(): Promise<void> {
    if (!projectId.value || refreshing.value) return
    refreshing.value = true
    try {
      workspace.value = await api.getWorkspace(projectId.value)
      error.value = ''
    } catch (err) {
      error.value = err instanceof Error ? err.message : 'Failed to refresh workspace'
    } finally {
      refreshing.value = false
    }
  }

  function reset(): void {
    workspace.value = null
    projectId.value = ''
    error.value = ''
    loading.value = false
    refreshing.value = false
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
