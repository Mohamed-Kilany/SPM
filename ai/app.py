"""SPM AI service.

A stateless, CPU-only semantic search and RAG service. The Go API supplies the
authorized project context for every request; this service never talks to the
SPM database directly.

The default answer engine is extractive and fully offline. An optional local
GGUF model can be enabled with llama-cpp-python (see README) without changing
the API contract.
"""

from __future__ import annotations

import math
import os
import re
import time
from collections import Counter
from typing import Any, Iterable

from fastapi import FastAPI, HTTPException
from pydantic import BaseModel, Field

APP_VERSION = "2.0.0"
STARTED_AT = time.time()

WORD_RE = re.compile(r"[a-zA-Z0-9_#./-]+")
STOP_WORDS = {
    "the", "a", "an", "and", "or", "but", "if", "then", "else", "for", "of",
    "to", "in", "on", "at", "by", "with", "from", "as", "is", "are", "was",
    "were", "be", "been", "being", "it", "its", "this", "that", "these",
    "those", "we", "you", "they", "he", "she", "i", "not", "no", "do", "does",
    "did", "can", "could", "should", "would", "will", "shall", "may", "might",
    "must", "have", "has", "had", "into", "than", "so", "such", "via", "per",
}


class Document(BaseModel):
    id: str
    type: str = "artifact"
    title: str = ""
    text: str = ""
    project_id: str = ""
    project_key: str = ""
    status: str = ""
    url: str = ""
    metadata: dict[str, Any] = Field(default_factory=dict)


class SearchRequest(BaseModel):
    query: str
    documents: list[Document] = Field(default_factory=list)
    top_k: int = 10


class SearchHit(BaseModel):
    id: str
    type: str
    title: str
    text: str
    project_id: str
    project_key: str
    status: str
    url: str
    score: float
    metadata: dict[str, Any] = Field(default_factory=dict)


class SearchResponse(BaseModel):
    query: str
    hits: list[SearchHit]
    engine: str
    elapsed_ms: float


class AskRequest(BaseModel):
    question: str
    documents: list[Document] = Field(default_factory=list)
    top_k: int = 6


class Citation(BaseModel):
    id: str
    title: str
    type: str
    url: str


class AskResponse(BaseModel):
    answer: str
    citations: list[Citation]
    confidence: float
    engine: str
    elapsed_ms: float


class InsightRequest(BaseModel):
    documents: list[Document] = Field(default_factory=list)
    limit: int = 8


class Insight(BaseModel):
    severity: str
    category: str
    title: str
    detail: str
    evidence: list[str] = Field(default_factory=list)
    action: str = ""


class InsightResponse(BaseModel):
    insights: list[Insight]
    engine: str


class HealthResponse(BaseModel):
    status: str
    version: str
    engine: str
    llm_enabled: bool
    uptime_seconds: float


app = FastAPI(title="SPM AI Service", version=APP_VERSION)


def tokenize(text: str) -> list[str]:
    return [token.lower() for token in WORD_RE.findall(text) if token.lower() not in STOP_WORDS]


def term_frequencies(tokens: Iterable[str]) -> Counter[str]:
    counts: Counter[str] = Counter()
    for token in tokens:
        counts[token] += 1
        if len(token) >= 4:
            counts[f"~{token[:4]}"] += 0.35
    return counts


def build_vectors(documents: list[Document]) -> tuple[list[Counter[str]], dict[str, float]]:
    token_sets = [term_frequencies(tokenize(f"{doc.title} {doc.text} {doc.type} {doc.status}")) for doc in documents]
    document_frequency: Counter[str] = Counter()
    for tokens in token_sets:
        for token in tokens:
            document_frequency[token] += 1
    total = max(1, len(documents))
    idf = {token: math.log((1 + total) / (1 + frequency)) + 1 for token, frequency in document_frequency.items()}
    return token_sets, idf


def vectorize(tokens: Counter[str], idf: dict[str, float]) -> dict[str, float]:
    return {token: (1 + math.log(count)) * idf.get(token, 1.0) for token, count in tokens.items() if count > 0}


def cosine(left: dict[str, float], right: dict[str, float]) -> float:
    if not left or not right:
        return 0.0
    dot = sum(value * right.get(token, 0.0) for token, value in left.items())
    left_norm = math.sqrt(sum(value * value for value in left.values()))
    right_norm = math.sqrt(sum(value * value for value in right.values()))
    if not left_norm or not right_norm:
        return 0.0
    return dot / (left_norm * right_norm)


def rank_documents(query: str, documents: list[Document], top_k: int) -> list[SearchHit]:
    if not query.strip() or not documents:
        return []
    query_tokens = tokenize(query)
    if not query_tokens:
        return []
    token_sets, idf = build_vectors(documents)
    query_vector = vectorize(term_frequencies(query_tokens), idf)
    scored: list[SearchHit] = []
    for doc, tokens in zip(documents, token_sets):
        doc_vector = vectorize(tokens, idf)
        score = cosine(query_vector, doc_vector)
        title_tokens = set(tokenize(doc.title))
        overlap = len(title_tokens.intersection(query_tokens))
        if overlap:
            score += 0.12 * overlap
        if query.lower() in f"{doc.title} {doc.text}".lower():
            score += 0.08
        if score <= 0:
            continue
        scored.append(
            SearchHit(
                id=doc.id,
                type=doc.type,
                title=doc.title,
                text=doc.text[:2000],
                project_id=doc.project_id,
                project_key=doc.project_key,
                status=doc.status,
                url=doc.url,
                score=round(score, 4),
                metadata=doc.metadata,
            )
        )
    scored.sort(key=lambda hit: hit.score, reverse=True)
    return scored[: max(1, min(top_k, 50))]


def sentence_split(text: str) -> list[str]:
    return [part.strip() for part in re.split(r"(?<=[.!?])\s+|\n+", text) if part.strip()]


def extractive_answer(question: str, hits: list[SearchHit]) -> tuple[str, float]:
    if not hits:
        return (
            "I could not find enough authorized project context to answer that. "
            "Try a more specific question or add linked requirements, tests, code or delivery evidence.",
            0.0,
        )
    question_tokens = set(tokenize(question))
    candidates: list[tuple[float, str, SearchHit]] = []
    for hit in hits:
        for sentence in sentence_split(hit.text):
            tokens = set(tokenize(sentence))
            overlap = len(tokens.intersection(question_tokens))
            if overlap:
                candidates.append((overlap + hit.score, sentence, hit))
    if not candidates:
        top = hits[0]
        return (
            f"The strongest match is {top.type} \"{top.title}\" ({top.status or 'no status'}). "
            "It contains the most relevant project context for this question.",
            min(0.55, hits[0].score),
        )
    candidates.sort(key=lambda entry: entry[0], reverse=True)
    selected = candidates[:3]
    lines = []
    seen: set[str] = set()
    for _, sentence, hit in selected:
        key = sentence[:120]
        if key in seen:
            continue
        seen.add(key)
        lines.append(f"- {sentence} ({hit.title})")
    answer = "Based on the authorized project context:\n" + "\n".join(lines)
    confidence = min(0.92, 0.35 + sum(entry[0] for entry in selected) / max(1, len(hits)) / 3)
    return answer, round(confidence, 2)


def llm_enabled() -> bool:
    return bool(os.getenv("SPM_AI_MODEL_PATH")) and os.getenv("SPM_AI_DISABLE_LLM", "").lower() not in {"1", "true", "yes"}


def engine_name() -> str:
    return "llama-cpp-cpu+tfidf" if llm_enabled() else "tfidf-extractive-cpu"


def maybe_llm_answer(question: str, hits: list[SearchHit], fallback: str) -> str:
    """Use a local GGUF model when available, otherwise return the extractive answer.

    The import is intentionally lazy so the default image stays small and starts
    instantly. See ai/README.md for enabling the optional model.
    """
    if not llm_enabled():
        return fallback
    try:
        from llama_cpp import Llama  # type: ignore
    except Exception:
        return fallback
    context = "\n\n".join(f"[{hit.type}] {hit.title}\n{hit.text[:1200]}" for hit in hits[:5])
    prompt = (
        "You are SPM's delivery copilot. Answer only from the provided project context. "
        "Cite artifact titles. If the context is insufficient, say so.\n\n"
        f"Context:\n{context}\n\nQuestion: {question}\nAnswer:"
    )
    try:
        model = Llama(
            model_path=os.environ["SPM_AI_MODEL_PATH"],
            n_ctx=int(os.getenv("SPM_AI_CONTEXT", "4096")),
            n_threads=max(1, int(os.getenv("SPM_AI_THREADS", str(os.cpu_count() or 2)))),
            n_gpu_layers=0,
            verbose=False,
        )
        output = model(prompt, max_tokens=int(os.getenv("SPM_AI_MAX_TOKENS", "384")), temperature=0.2)
        return output["choices"][0]["text"].strip() or fallback
    except Exception:
        return fallback


@app.get("/health", response_model=HealthResponse)
def health() -> HealthResponse:
    return HealthResponse(
        status="ok",
        version=APP_VERSION,
        engine=engine_name(),
        llm_enabled=llm_enabled(),
        uptime_seconds=round(time.time() - STARTED_AT, 2),
    )


@app.post("/semantic-search", response_model=SearchResponse)
def semantic_search(request: SearchRequest) -> SearchResponse:
    started = time.perf_counter()
    hits = rank_documents(request.query, request.documents, request.top_k)
    return SearchResponse(
        query=request.query,
        hits=hits,
        engine=engine_name(),
        elapsed_ms=round((time.perf_counter() - started) * 1000, 2),
    )


@app.post("/ask", response_model=AskResponse)
def ask(request: AskRequest) -> AskResponse:
    started = time.perf_counter()
    hits = rank_documents(request.question, request.documents, request.top_k)
    answer, confidence = extractive_answer(request.question, hits)
    answer = maybe_llm_answer(request.question, hits, answer)
    citations = [
        Citation(id=hit.id, title=hit.title, type=hit.type, url=hit.url)
        for hit in hits[:5]
    ]
    return AskResponse(
        answer=answer,
        citations=citations,
        confidence=confidence,
        engine=engine_name(),
        elapsed_ms=round((time.perf_counter() - started) * 1000, 2),
    )


@app.post("/insights", response_model=InsightResponse)
def insights(request: InsightRequest) -> InsightResponse:
    documents = request.documents
    results: list[Insight] = []

    blocked = [doc for doc in documents if doc.status in {"blocked", "failed", "degraded"}]
    if blocked:
        results.append(
            Insight(
                severity="high",
                category="delivery-risk",
                title=f"{len(blocked)} blocked or failing artifact(s)",
                detail="Blocked work and failed delivery evidence are the highest-risk items in this project.",
                evidence=[f"{doc.type}: {doc.title}" for doc in blocked[:5]],
                action="Open the blocked items, assign an owner, and record the unblock plan.",
            )
        )

    untested = [
        doc for doc in documents
        if doc.type == "feature" and not any(
            other.type == "test_case" and other.metadata.get("feature_id") == doc.id
            for other in documents
        )
    ]
    if untested:
        results.append(
            Insight(
                severity="medium",
                category="test-coverage",
                title=f"{len(untested)} feature(s) without test coverage",
                detail="Features without linked test cases cannot produce release evidence.",
                evidence=[doc.title for doc in untested[:5]],
                action="Create test cases or link existing automated tests to each feature.",
            )
        )

    critical_bugs = [
        doc for doc in documents
        if doc.type == "work_item" and doc.metadata.get("item_type") == "bug"
        and doc.metadata.get("priority") == "critical"
        and doc.status not in {"done", "closed", "verified", "cancelled"}
    ]
    if critical_bugs:
        results.append(
            Insight(
                severity="high",
                category="quality",
                title=f"{len(critical_bugs)} open critical bug(s)",
                detail="Critical defects block release readiness and should be triaged first.",
                evidence=[doc.title for doc in critical_bugs[:5]],
                action="Triage each critical bug and link the fixing branch or pull request.",
            )
        )

    stale = [
        doc for doc in documents
        if doc.status in {"in_progress", "in_review", "in_qa"} and doc.metadata.get("stale_days", 0) >= 7
    ]
    if stale:
        results.append(
            Insight(
                severity="medium",
                category="flow",
                title=f"{len(stale)} stale in-flight artifact(s)",
                detail="These items have not moved recently and may be stuck.",
                evidence=[doc.title for doc in stale[:5]],
                action="Review the next step with the owner and update status or dependencies.",
            )
        )

    if not results:
        results.append(
            Insight(
                severity="low",
                category="health",
                title="No urgent delivery risks detected",
                detail="The supplied context has no blocked items, critical bugs or obvious coverage gaps.",
                evidence=[],
                action="Continue linking code, tests and deployment evidence as work progresses.",
            )
        )

    return InsightResponse(insights=results[: max(1, min(request.limit, 20))], engine=engine_name())


@app.get("/")
def root() -> dict[str, Any]:
    return {
        "service": "spm-ai",
        "version": APP_VERSION,
        "docs": "/docs",
        "engine": engine_name(),
        "endpoints": ["/health", "/semantic-search", "/ask", "/insights"],
    }
