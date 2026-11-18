export interface AiCitation {
  id: string
  title: string
  type: string
  url: string
}

export interface AiSearchHit {
  id: string
  type: string
  title: string
  text: string
  project_id: string
  project_key: string
  status: string
  url: string
  score: number
  metadata: Record<string, unknown>
}

export interface AiSearchResponse {
  query: string
  hits: AiSearchHit[]
  engine: string
  elapsed_ms: number
}

export interface AiAskResponse {
  answer: string
  citations: AiCitation[]
  confidence: number
  engine: string
  elapsed_ms: number
}

export interface AiInsight {
  severity: 'high' | 'medium' | 'low'
  category: string
  title: string
  detail: string
  evidence: string[]
  action: string
}

export interface AiInsightResponse {
  insights: AiInsight[]
  engine: string
}
