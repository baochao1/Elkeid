#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -ne 3 ]; then
    echo 'Usage: bash verify-scanner.sh /path/to/elkeid-agent /path/to/mock-manager /path/to/prepared-fixtures' >&2
    exit 2
fi

agent=$(realpath "$1")
manager=$(realpath "$2")
fixtures=$(realpath "$3")
repo=$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)
work=$(mktemp -d)
agent_pid=
manager_pid=

cleanup() {
    if [ -n "$agent_pid" ]; then
        kill -TERM "$agent_pid" 2>/dev/null || true
        wait "$agent_pid" 2>/dev/null || true
    fi
    if [ -n "$manager_pid" ]; then
        kill -TERM "$manager_pid" 2>/dev/null || true
        wait "$manager_pid" 2>/dev/null || true
    fi
    echo "Logs: $work"
}
trap cleanup EXIT

wait_for() {
    local pattern="$1" deadline=$((SECONDS + 85))
    until grep -Eq "$pattern" manager.log; do
        if ! kill -0 "$manager_pid" 2>/dev/null || (( SECONDS >= deadline )); then
            echo "Missing evidence in manager.log: $pattern" >&2
            tail -n 40 manager.log >&2
            return 1
        fi
        sleep 0.2
    done
    grep -Em1 "$pattern" manager.log
}

cd "$work"
printf 'Elkeid local demo upload\n' > demo-upload.txt
"$manager" -repo "$repo" -scanner-fixture-dir "$fixtures" > manager.log 2>&1 &
manager_pid=$!
wait_for 'event=ready '

env -u service_type NO_PROXY=127.0.0.1,localhost no_proxy=127.0.0.1,localhost \
    specified_region=default specified_idc=default SPECIFIED_AGENT_ID=local-demo-scanner \
    "$agent" > agent.stdout 2>&1 &
agent_pid=$!

wait_for 'event=plugin_heartbeat data_type=1001 name="scanner"'
wait_for 'event=scanner_report data_type=6003 token="demo-hit" types="Elkeid" class="LocalDemo" name="Marker" error=""'
wait_for 'event=scanner_report data_type=6003 token="demo-clean" types="not_detected" class="" name="" error=""'
echo 'PASS: Agent activated the real scanner and transferred both 6003 results'
