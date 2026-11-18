from fastapi.testclient import TestClient

from app import app

client = TestClient(app)


def sample_documents() -> list[dict]:
    return [
        {
            "id": "1",
            "type": "work_item",
            "title": "Fix checkout timeout",
            "text": "Checkout requests time out after 30 seconds. Add retries and a circuit breaker.",
            "status": "blocked",
            "metadata": {"item_type": "bug", "priority": "critical"},
        },
        {
            "id": "2",
            "type": "feature",
            "title": "Checkout redesign",
            "text": "Redesign the checkout flow with saved payment methods and address validation.",
            "status": "in_progress",
            "metadata": {},
        },
        {
            "id": "3",
            "type": "test_case",
            "title": "Checkout timeout regression",
            "text": "Verify checkout retries when the payment provider times out.",
            "status": "ready",
            "metadata": {"feature_id": "2"},
        },
    ]


def test_health_is_cpu_extractive() -> None:
    response = client.get("/health")
    assert response.status_code == 200
    body = response.json()
    assert body["status"] == "ok"
    assert "cpu" in body["engine"]


def test_semantic_search_ranks_relevant_documents() -> None:
    response = client.post(
        "/semantic-search",
        json={"query": "checkout timeout retries", "documents": sample_documents(), "top_k": 2},
    )
    assert response.status_code == 200
    hits = response.json()["hits"]
    assert hits
    assert hits[0]["title"] in {"Fix checkout timeout", "Checkout timeout regression"}


def test_ask_returns_citations() -> None:
    response = client.post(
        "/ask",
        json={"question": "What is blocking checkout?", "documents": sample_documents()},
    )
    assert response.status_code == 200
    body = response.json()
    assert body["citations"]
    assert body["answer"]
    assert 0 <= body["confidence"] <= 1


def test_insights_detect_critical_and_coverage_gaps() -> None:
    response = client.post("/insights", json={"documents": sample_documents()})
    assert response.status_code == 200
    insights = response.json()["insights"]
    categories = {entry["category"] for entry in insights}
    assert "quality" in categories
    assert "delivery-risk" in categories

