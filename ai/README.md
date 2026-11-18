# SPM AI Service

Stateless, CPU-only semantic search and retrieval-augmented generation (RAG) for
SPM project data. The Go API supplies authorized project context with every
request. This service never connects to PostgreSQL, NATS or Gitea.

## Endpoints

| Endpoint | Purpose |
| --- | --- |
| `GET /health` | Liveness, engine mode and uptime |
| `POST /semantic-search` | Rank supplied artifacts against a natural-language query |
| `POST /ask` | Answer a question from supplied artifacts with citations |
| `POST /insights` | Deterministic delivery risks, coverage gaps and recommendations |

## Runtime model

The default image uses a TF-IDF/cosine semantic index plus extractive RAG. It is
fully offline, CPU-only, deterministic and starts instantly. No model download is
required.

An optional local GGUF model can be enabled for generative answers:

```bash
docker compose build --build-arg AI_LLM=1 ai_service
# mount a GGUF file and set:
SPM_AI_MODEL_PATH=/models/tiny.gguf
SPM_AI_THREADS=4
```

`llama-cpp-python` is configured with `n_gpu_layers=0`; it never requires CUDA.
If the model is missing or fails to load, the service falls back to the
extractive answer engine automatically.

## Design constraints

- Stateless: all context is in the request payload.
- Authorized context: only the Go API decides which project artifacts are sent.
- Offline-first: no external AI provider calls.
- CPU-only: no GPU libraries in the default image.
- Bounded input: callers should send concise, already-authorized context.
