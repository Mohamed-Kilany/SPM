#!/usr/bin/env bash
# Real-dependency integration gate.
#
# Starts the isolated PostgreSQL, NATS, Gitea and API services from
# docker-compose.test.yml, proves that a fresh database received every
# migration, rehearses an upgrade from the pull request base commit, and then
# runs the tagged Go integration suite.
#
# Required on the runner: bash, curl, docker compose and a Go toolchain.
# BASE_SHA is the previous master commit. When it is unset (local run), the
# upgrade rehearsal is skipped; the migration-from-zero and integration checks
# still run.
#
# Exit status is non-zero on the first failed stage. On failure the script
# writes integration-stack.log (compose logs) and integration-upgrade.log (the
# upgraded API log) for the workflow to upload. Set KEEP_STACK=1 to leave the
# stack running for debugging.

set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

COMPOSE_FILE="docker-compose.test.yml"
COMPOSE=(docker compose -f "$COMPOSE_FILE")

BASE_SHA="${BASE_SHA:-}"
KEEP_STACK="${KEEP_STACK:-0}"
TMP_ROOT="${RUNNER_TEMP:-/tmp}"
POSTGRES_PORT="${SPM_TEST_POSTGRES_PORT:-55432}"
NATS_PORT="${SPM_TEST_NATS_PORT:-54222}"
UPGRADE_API_PORT="${SPM_TEST_UPGRADE_API_PORT:-18080}"
UPGRADE_METRICS_PORT="${SPM_TEST_UPGRADE_METRICS_PORT:-19090}"
UPGRADE_SENTINEL_ID="00000000-0000-0000-0000-00000000c001"
UPGRADE_ADMIN_ID="00000000-0000-0000-0000-000000000001"
UPGRADE_DB="spm_upgrade"

api_pid=""

cleanup() {
    local status=$?
    trap - EXIT

    if [ "$status" -ne 0 ]; then
        echo "::group::Integration stack logs"
        "${COMPOSE[@]}" logs --no-color > integration-stack.log 2>&1 || true
        cat integration-stack.log || true
        if [ -f integration-upgrade.log ]; then
            echo "::group::Upgraded API log"
            cat integration-upgrade.log || true
            echo "::endgroup::"
        fi
        echo "::endgroup::"
    fi

    if [ -n "$api_pid" ] && kill -0 "$api_pid" 2>/dev/null; then
        kill "$api_pid" 2>/dev/null || true
        wait "$api_pid" 2>/dev/null || true
    fi

    if [ "$KEEP_STACK" != "1" ]; then
        "${COMPOSE[@]}" down -v --remove-orphans >/dev/null 2>&1 || true
    else
        echo "KEEP_STACK=1: leaving the integration stack running."
    fi

    exit "$status"
}
trap cleanup EXIT

psql_main() {
    "${COMPOSE[@]}" exec -T test_postgres psql -U spm_test -d "$1" "${@:2}"
}

psql_value() {
    psql_main "$1" -tAc "$2" | tr -d '[:space:]'
}

echo "Validating the isolated test stack..."
"${COMPOSE[@]}" config --quiet

echo "Building the API and integration runner images..."
"${COMPOSE[@]}" build test_api_backend test_runner

echo "Starting PostgreSQL, NATS, Gitea and the API..."
"${COMPOSE[@]}" up -d --wait --wait-timeout 300 \
    test_postgres test_nats test_gitea test_api_backend

expected_migrations="$(find api/internal/migrate/sql -maxdepth 1 -type f -name '*.sql' ! -name '000_*' | wc -l | tr -d '[:space:]')"
actual_migrations="$(psql_value spm_test 'SELECT count(*) FROM schema_migrations')"
if [ "$actual_migrations" != "$expected_migrations" ]; then
    echo "Migration-from-zero failed: applied $actual_migrations of $expected_migrations migrations."
    psql_main spm_test -c 'SELECT version FROM schema_migrations ORDER BY version' || true
    exit 1
fi
echo "Migration-from-zero: $actual_migrations migrations applied and recorded."

if [ -z "$BASE_SHA" ]; then
    echo "BASE_SHA is not set; skipping the previous-release upgrade rehearsal."
else
    if ! git cat-file -e "$BASE_SHA^{commit}" 2>/dev/null; then
        echo "BASE_SHA $BASE_SHA is not a commit in this checkout."
        exit 1
    fi
    echo "Rehearsing an upgrade from $BASE_SHA..."
    mapfile -t previous_files < <(
        git ls-tree -r --name-only "$BASE_SHA" -- api/internal/migrate/sql \
            | grep -E '^api/internal/migrate/sql/[0-9]{3}_.*\.sql$' \
            | grep -v '/000_' \
            | sort
    )
    if [ "${#previous_files[@]}" -eq 0 ]; then
        echo "No previous migrations found at $BASE_SHA; skipping the upgrade rehearsal."
    else
        previous_sql="$TMP_ROOT/previous-migrations.sql"
        seed_sql="$TMP_ROOT/upgrade-seed.sql"
        : > "$previous_sql"

        for file in "${previous_files[@]}"; do
            git show "$BASE_SHA:$file" >> "$previous_sql"
            printf '\n' >> "$previous_sql"
        done

        psql_main spm_test -v ON_ERROR_STOP=1 -q \
            -c "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '$UPGRADE_DB' AND pid <> pg_backend_pid()" \
            -c "DROP DATABASE IF EXISTS $UPGRADE_DB" \
            -c "CREATE DATABASE $UPGRADE_DB"

        psql_main "$UPGRADE_DB" -v ON_ERROR_STOP=1 -q < "$previous_sql"

        {
            echo "CREATE TABLE IF NOT EXISTS schema_migrations (version VARCHAR(255) PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW());"
            echo -n "INSERT INTO schema_migrations(version) VALUES "
            for i in "${!previous_files[@]}"; do
                if [ "$i" -gt 0 ]; then
                    echo -n ", "
                fi
                printf "('%s')" "$(basename "${previous_files[$i]}")"
            done
            echo " ON CONFLICT (version) DO NOTHING;"
            echo "INSERT INTO projects (id, name, owner_id) VALUES ('$UPGRADE_SENTINEL_ID', 'Upgrade rehearsal sentinel', '$UPGRADE_ADMIN_ID') ON CONFLICT (id) DO NOTHING;"
        } > "$seed_sql"

        psql_main "$UPGRADE_DB" -v ON_ERROR_STOP=1 -q < "$seed_sql"

        (
            cd api
            CGO_ENABLED=0 go build -trimpath -o "$TMP_ROOT/spm-upgrade-api" ./cmd/api
        )

        upgrade_log="$ROOT_DIR/integration-upgrade.log"
        : > "$upgrade_log"
        DATABASE_URL="postgres://spm_test:spm_test_password@127.0.0.1:${POSTGRES_PORT}/${UPGRADE_DB}?sslmode=disable" \
            NATS_URL="nats://127.0.0.1:${NATS_PORT}" \
            PORT="$UPGRADE_API_PORT" \
            METRICS_ADDR="127.0.0.1:${UPGRADE_METRICS_PORT}" \
            JWT_SECRET="ci-upgrade-secret" \
            GITEA_BASE_URL="http://127.0.0.1:3000" \
            GITEA_API_TOKEN="ci-upgrade-token" \
            GITEA_ORG="spm" \
            ATTACHMENTS_DIR="$TMP_ROOT/spm-upgrade-attachments" \
            "$TMP_ROOT/spm-upgrade-api" >> "$upgrade_log" 2>&1 &
        api_pid=$!

        healthy=""
        for _ in $(seq 1 90); do
            if curl -fsS "http://127.0.0.1:${UPGRADE_API_PORT}/health" >/dev/null 2>&1; then
                healthy="yes"
                break
            fi
            if ! kill -0 "$api_pid" 2>/dev/null; then
                echo "The upgraded API exited before becoming healthy."
                cat "$upgrade_log"
                exit 1
            fi
            sleep 1
        done
        if [ "$healthy" != "yes" ]; then
            echo "The upgraded API did not become healthy within 90 seconds."
            cat "$upgrade_log"
            exit 1
        fi

        upgraded_migrations="$(psql_value "$UPGRADE_DB" 'SELECT count(*) FROM schema_migrations')"
        if [ "$upgraded_migrations" != "$expected_migrations" ]; then
            echo "Upgrade rehearsal failed: applied $upgraded_migrations of $expected_migrations migrations."
            psql_main "$UPGRADE_DB" -c 'SELECT version FROM schema_migrations ORDER BY version' || true
            exit 1
        fi
        sentinel="$(psql_value "$UPGRADE_DB" "SELECT count(*) FROM projects WHERE id = '$UPGRADE_SENTINEL_ID'")"
        if [ "$sentinel" != "1" ]; then
            echo "Upgrade rehearsal failed: the pre-upgrade sentinel row was not preserved."
            exit 1
        fi

        kill "$api_pid" 2>/dev/null || true
        wait "$api_pid" 2>/dev/null || true
        api_pid=""
        echo "Upgrade rehearsal: $upgraded_migrations migrations applied and the sentinel row survived."
    fi
fi

echo "Running the Go integration suite against the live stack..."
set -o pipefail
"${COMPOSE[@]}" run --rm --no-deps test_runner 2>&1 | tee integration-tests.log

if ! grep -q -- '--- PASS' integration-tests.log; then
    echo "The integration suite exited successfully but reported no passing tests."
    exit 1
fi
passes="$(grep -c -- '--- PASS' integration-tests.log || true)"
skips="$(grep -c -- '--- SKIP' integration-tests.log || true)"
echo "Integration suite: $passes passed, $skips skipped."

echo "Integration gate passed."
