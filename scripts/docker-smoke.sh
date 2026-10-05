#!/bin/sh
# Run only in a disposable test environment with Docker, Compose and just.
set -eu
cd "$(dirname "$0")/.."
export COMPOSE_PROJECT_NAME="yip-smoke-$$"
export YIP_RUNNER_URL=https://localhost:7443
cleanup() {
    just docker logs --no-color hub || true
    just docker down || true
}
trap cleanup EXIT
just docker up --build -d
ready=false
for attempt in $(seq 1 60); do
    if curl --fail --silent --max-time 3 http://127.0.0.1:7420/healthz > /dev/null; then
        ready=true
        break
    fi
    sleep 1
done
[ "$ready" = true ] || { echo 'Hub did not become healthy' >&2; exit 1; }
curl --fail --silent --max-time 3 http://127.0.0.1:7420/ | grep -q '/assets/'
curl --fail --silent --max-time 3 http://127.0.0.1:7420/v1/setup | grep -q '"needsSetup":true'
just docker exec -T hub yip version
# Exercise literal arguments through just, Compose and the container process.
literal='two words; $(exit 9)'
actual=$(just --quiet docker exec -T hub printf '%s' "$literal")
[ "$actual" = "$literal" ] || { echo 'Docker arguments changed in transit' >&2; exit 1; }
