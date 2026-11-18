export interface SearchWorkItem {
  id: string
  key: string
  title: string
  description: string
  status: string
  priority: string
  type: string
  feature: string
  assignee: string
  project_id: string
  project_key: string
  project_name: string
  updated_at: string
}

export interface SearchFeature {
  id: string
  title: string
  description: string
  status: string
  project_id: string
  project_key: string
  project_name: string
  updated_at: string
}

export interface SearchTestCase {
  id: string
  title: string
  description: string
  status: string
  type: string
  feature: string
  project_id: string
  project_key: string
  project_name: string
  updated_at: string
}

export interface SearchProject {
  id: string
  name: string
  key_prefix: string
  status: string
}

export interface SearchResponse {
  query: string
  work_items: SearchWorkItem[]
  features: SearchFeature[]
  test_cases: SearchTestCase[]
  projects: SearchProject[]
  total: number
  error?: string
}
