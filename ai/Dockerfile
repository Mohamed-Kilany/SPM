FROM python:3.12-slim AS runtime

ARG AI_LLM=0
ARG SPM_AI_VERSION=dev
ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    PIP_NO_CACHE_DIR=1 \
    SPM_AI_VERSION=$SPM_AI_VERSION \
    PORT=8090

WORKDIR /app

RUN addgroup --system spm && adduser --system --ingroup spm spm

COPY ai/requirements.txt ./requirements.txt
COPY ai/requirements-llm.txt ./requirements-llm.txt
RUN pip install --no-cache-dir -r requirements.txt
RUN if [ "$AI_LLM" = "1" ]; then \
      apt-get update && apt-get install -y --no-install-recommends build-essential cmake libgomp1 && \
      pip install --no-cache-dir -r requirements-llm.txt && \
      apt-get purge -y build-essential cmake && apt-get autoremove -y && rm -rf /var/lib/apt/lists/*; \
    fi

COPY ai/app.py ./app.py
COPY ai/README.md ./README.md

USER spm

EXPOSE 8090

HEALTHCHECK --interval=10s --timeout=3s --start-period=10s --retries=5 \
  CMD python -c "import urllib.request; urllib.request.urlopen('http://127.0.0.1:8090/health', timeout=2)"

CMD ["uvicorn", "app:app", "--host", "0.0.0.0", "--port", "8090", "--workers", "1"]
