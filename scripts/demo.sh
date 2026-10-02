#!/usr/bin/env bash
# Runs the connector against the fake Freshdesk server and prints an MCP
# session. Usage: scripts/demo.sh [basic|flaky|outage]
set -euo pipefail
cd "$(dirname "$0")/.."
mode="${1:-basic}"
port=18089

fake_flags=()
case "$mode" in
  basic)  ;;
  flaky)  fake_flags=(-every-429 3 -every-500 4) ;;
  outage) fake_flags=(-outage 4s) ;;
  *) echo "unknown mode: $mode" >&2; exit 2 ;;
esac

./bin/fake-freshdesk -addr "127.0.0.1:$port" "${fake_flags[@]}" 2>/dev/null &
fake_pid=$!
trap 'kill $fake_pid 2>/dev/null || true' EXIT
sleep 0.5

export FRESHDESK_API_KEY=demo-key
export FRESHDESK_BASE_URL="http://127.0.0.1:$port/api/v2"
export FRESHDESK_BACKOFF_BASE=200ms
if [ "$mode" = outage ]; then
  export FRESHDESK_MAX_RETRIES=0 FRESHDESK_BREAKER_THRESHOLD=3 FRESHDESK_BREAKER_COOLDOWN=2s FRESHDESK_CACHE_TTL=0s
fi

req() { printf '{"jsonrpc":"2.0","id":%s,"method":"%s","params":%s}\n' "$1" "$2" "$3"; }
call() { req "$1" tools/call "{\"name\":\"$2\",\"arguments\":$3}"; }

session() {
  req 1 initialize '{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"demo","version":"1"}}'
  echo '{"jsonrpc":"2.0","method":"notifications/initialized"}'
  case "$mode" in
    basic)
      call 2 freshdesk_search_tickets '{"query":"status:2 AND tag:'"'"'refund'"'"'"}'
      call 3 freshdesk_get_ticket '{"ticket_id":201}'
      call 4 freshdesk_ticket_brief '{"ticket_id":201}'
      call 5 freshdesk_find_similar_tickets '{"ticket_id":201,"updated_since":"2020-01-01"}'
      call 6 freshdesk_get_ticket '{"ticket_id":999}'
      ;;
    flaky)
      for i in 2 3 4 5 6 7; do call "$i" freshdesk_get_ticket "{\"ticket_id\":$((199 + i))}"; done
      ;;
    outage)
      for i in 2 3 4 5; do call "$i" freshdesk_get_ticket '{"ticket_id":201}'; sleep 0.3; done
      sleep 3
      call 6 freshdesk_get_ticket '{"ticket_id":201}'
      ;;
  esac
}

# Responses go to stdout, one per line; logs and the audit trail go to stderr.
session | ./bin/freshdesk-mcp 2> >(sed 's/^/  [stderr] /' >&2) | while IFS= read -r line; do
  if command -v python3 >/dev/null; then
    printf '%s' "$line" | python3 -c '
import json, sys
m = json.load(sys.stdin)
r = m.get("result", {})
if "content" in r:
    t = r["content"][0]["text"]
    flag = "ERROR " if r.get("isError") else ""
    print("<- id=%s %s%s" % (m["id"], flag, t if len(t) < 700 else t[:700] + " ..."))
else:
    print("<- id=%s %s" % (m.get("id"), json.dumps(r)[:200]))'
  else
    echo "<- $line"
  fi
done
sleep 0.3
