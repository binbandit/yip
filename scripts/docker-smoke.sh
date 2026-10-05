#!/bin/sh
# Run only in a disposable test environment with Docker, Compose and just.
set -eu
cd "$(dirname "$0")/.."
export COMPOSE_PROJECT_NAME="yip-smoke-$$"
export YIP_RUNNER_URL=https://localhost:7443
tools=$(mktemp -d)
cleanup() {
    just docker logs --no-color hub || true
    just docker down || true
    rm -rf "$tools"
}
trap cleanup EXIT
check_hub() {
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
}
just docker up --build -d
check_hub
just docker exec -T hub yip version
# Exercise literal arguments through just, Compose and the container process.
literal='two words; $(exit 9)'
actual=$(just docker exec -T hub printf '%s' "$literal")
[ "$actual" = "$literal" ] || { echo 'Docker arguments changed in transit' >&2; exit 1; }
just docker down

# Local compilation must produce an executable for the Linux runtime, including
# the actual web assets. Proxy settings stay with the host Go tool.
# Work shells often restrict new files to their owner; the container uses a
# separate non-root identity and must still be able to execute the binary.
(umask 077; VERSION=docker-local-smoke just docker-build)
image=$(docker image inspect --format '{{.Id}}' yip-hub:local)
for tool in go npm; do
    printf '#!/bin/sh\necho "run unexpectedly tried to compile" >&2\nexit 99\n' > "$tools/$tool"
    chmod +x "$tools/$tool"
done
for run in 1 2; do
    PATH="$tools:$PATH" just docker-run -d
    check_hub
    just docker exec -T hub yip version | grep -q docker-local-smoke
    container=$(just docker ps -q hub)
    [ "$(docker inspect --format '{{.Image}}' "$container")" = "$image" ]
    [ "$(docker image inspect --format '{{.Id}}' yip-hub:local)" = "$image" ]
    just docker down
done
