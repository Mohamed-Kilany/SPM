# The server base is parameterised so the isolated CI stack can keep using its
# pinned nginx version while production uses the OpenTelemetry build.
ARG NGINX_IMAGE=nginx:1.30.5-alpine-otel

# ── Stage 1: Build the Vue SPA ───────────────────────────────────────────────
# Node 24 is the supported LTS required by the current Vite/Vitest toolchain.
FROM node:24-alpine AS builder

WORKDIR /app

# Test files and local build output must never leak into the image context.
COPY package.json package-lock.json ./
RUN npm ci --no-audit --no-fund --prefer-offline --fetch-retries=5 --fetch-retry-mintimeout=20000 --fetch-retry-maxtimeout=120000

COPY . .
RUN npm run build

# ── Stage 2: Production static server ────────────────────────────────────────
FROM ${NGINX_IMAGE} AS runtime

ENV API_UPSTREAM=api_backend:8080

# The official nginx entrypoint expands ${API_UPSTREAM} into
# /etc/nginx/conf.d/default.conf at container start.
COPY nginx.conf /etc/nginx/templates/default.conf.template
COPY security-headers.conf /etc/nginx/security-headers.conf
COPY --from=builder /app/dist /usr/share/nginx/html

EXPOSE 80

HEALTHCHECK --interval=10s --timeout=5s --start-period=10s --retries=5 \
  CMD wget -qO- http://localhost/ >/dev/null 2>&1 || exit 1

ENTRYPOINT ["/docker-entrypoint.sh"]
CMD ["nginx", "-g", "daemon off;"]
