#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -ne 2 ]; then
    echo 'Usage: bash verify.sh /absolute/path/elkeid-agent /absolute/path/mock-manager' >&2
    exit 2
fi

agent="$(realpath "$1")"
manager="$(realpath "$2")"
repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
runs_dir="${ELKEID_DEMO_RUNS_DIR:-$HOME/elkeid-local-demo/runs}"
mkdir -p "$runs_dir"
runs_dir="$(realpath "$runs_dir")"
work="$(mktemp -d "$runs_dir/run.XXXXXXXX")"
agent_pid=
manager_pid=

cleanup() {
    if [ -n "$agent_pid" ]; then
        kill -TERM "$agent_pid" 2>/dev/null || true
        local deadline=$((SECONDS + 12))
        while kill -0 "$agent_pid" 2>/dev/null && (( SECONDS < deadline )); do
            sleep 0.2
        done
        kill -KILL "$agent_pid" 2>/dev/null || true
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
    local file="$1" pattern="$2" pid="$3" deadline=$((SECONDS + 85))
    until grep -Eq "$pattern" "$file" 2>/dev/null; do
        if ! kill -0 "$pid" 2>/dev/null || (( SECONDS >= deadline )); then
            echo "Missing evidence in $file: $pattern" >&2
            tail -n 25 "$file" >&2 || true
            return 1
        fi
        sleep 0.2
    done
    grep -Em1 "$pattern" "$file"
}

cd "$work"
printf 'Elkeid local demo upload\n' > demo-upload.txt
"$manager" -repo "$repo" > manager.log 2>&1 &
manager_pid=$!
wait_for manager.log 'event=ready ' "$manager_pid"
env -u service_type NO_PROXY=127.0.0.1,localhost no_proxy=127.0.0.1,localhost specified_region=default specified_idc=default \
    SPECIFIED_AGENT_ID=local-demo-acceptance "$agent" > agent.stdout 2>&1 &
agent_pid=$!

wait_for manager.log 'event=agent_connected agent_id="local-demo-acceptance"' "$manager_pid"
wait_for manager.log 'event=agent_heartbeat data_type=1000 ' "$manager_pid"
wait_for manager.log 'event=config_sent plugin=demo-fixture ' "$manager_pid"
wait_for manager.log 'event=fixture_download ' "$manager_pid"
wait_for log/elkeid-agent.log 'plugin has been loaded' "$agent_pid"
wait_for manager.log 'event=fixture_record data_type=9990 message="local fixture started" detail="loopback-fixture"' "$manager_pid"
wait_for manager.log 'event=upload_verified token=local-demo-upload bytes=25 ' "$manager_pid"
wait_for log/elkeid-agent.log 'upload completed:.*SUCCESS' "$agent_pid"
wait_for manager.log 'event=plugin_heartbeat data_type=1001 name="demo-fixture"' "$manager_pid"
echo 'PASS: mTLS, agent and plugin heartbeat, component activation, payload decode and fixture upload'
