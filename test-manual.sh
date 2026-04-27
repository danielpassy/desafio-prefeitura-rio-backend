#!/usr/bin/env bash
# Roteiro de teste manual. Mostra comando, título e resposta crua.
# Uso:
#   ./test-manual.sh         # menu interativo
#   ./test-manual.sh 5       # cenário 5
#   ./test-manual.sh all     # todos (pula WS e DLQ)

set -u

BASE_URL="${BASE_URL:-http://localhost:8081}"
IDP_URL="${IDP_URL:-http://localhost:15423}"
CPF="${CPF:-12345678901}"
CPF_OUTRO="${CPF_OUTRO:-99999999999}"
WEBHOOK_SECRET="$(grep ^WEBHOOK_SECRET .env | cut -d= -f2)"

bold() { printf "\n\033[1m=== %s ===\033[0m\n" "$*"; }
cmd()  { printf "\033[2m\$ %s\033[0m\n" "$*"; }

token_for() {
  curl -s -X POST "$IDP_URL/default/token" \
    -d "grant_type=client_credentials&client_id=$1&client_secret=any" \
    | python3 -c "import sys,json;print(json.load(sys.stdin)['access_token'])"
}

sign() { echo -n "$1" | openssl dgst -sha256 -hmac "$WEBHOOK_SECRET" -hex | awk '{print $2}'; }

ensure_token() { [ -n "${TOKEN:-}" ] || TOKEN=$(token_for "$CPF"); }

# ---------- cenários ----------

s1() {
  bold "1) Webhook caminho feliz"
  local tid="CH-$(date +%s)-$RANDOM"
  local body='{"ticket_id":"'$tid'","type":"status_change","cpf":"'$CPF'","previous_status":"open","new_status":"in_progress","title":"Buraco na Copa","timestamp":"'$(date -u +%FT%TZ)'"}'
  local sig; sig=$(sign "$body")
  cmd "curl -sSi -X POST $BASE_URL/webhook -H 'X-Signature-256: sha256=$sig' -d '$body'"
  curl -sSi -X POST "$BASE_URL/webhook" -H "Content-Type: application/json" -H "X-Signature-256: sha256=$sig" -d "$body"
  echo
}

s2() {
  bold "2) Webhook idempotente (mesmo body 2x — espera 201 depois 200)"
  local body='{"ticket_id":"CH-001","type":"status_change","cpf":"'$CPF'","previous_status":"open","new_status":"in_progress","title":"Buraco na Copa","timestamp":"2026-04-27T10:00:00Z"}'
  local sig; sig=$(sign "$body")
  cmd "curl -sSi -X POST $BASE_URL/webhook -H 'X-Signature-256: sha256=$sig' -d '$body'   # 1ª"
  curl -sSi -X POST "$BASE_URL/webhook" -H "Content-Type: application/json" -H "X-Signature-256: sha256=$sig" -d "$body"
  echo; echo
  cmd "# 2ª chamada idêntica:"
  curl -sSi -X POST "$BASE_URL/webhook" -H "Content-Type: application/json" -H "X-Signature-256: sha256=$sig" -d "$body"
  echo
}

s3() {
  bold "3) Webhook assinatura inválida"
  local body='{"ticket_id":"CH-X","type":"status_change","cpf":"'$CPF'","previous_status":"open","new_status":"in_progress","title":"x","timestamp":"2026-04-27T10:00:00Z"}'
  cmd "curl -sSi -X POST $BASE_URL/webhook -H 'X-Signature-256: sha256=deadbeef' -d '$body'"
  curl -sSi -X POST "$BASE_URL/webhook" -H "Content-Type: application/json" -H "X-Signature-256: sha256=deadbeef" -d "$body"
  echo
}

s4() {
  bold "4) Webhook sem header de assinatura"
  local body='{"ticket_id":"CH-X","type":"status_change","cpf":"'$CPF'","previous_status":"open","new_status":"in_progress","title":"x","timestamp":"2026-04-27T10:00:00Z"}'
  cmd "curl -sSi -X POST $BASE_URL/webhook -d '$body'"
  curl -sSi -X POST "$BASE_URL/webhook" -H "Content-Type: application/json" -d "$body"
  echo
}

s5() {
  bold "5) Webhook CPF inválido (3 dígitos)"
  local body='{"ticket_id":"CH-Y","type":"status_change","cpf":"123","previous_status":"open","new_status":"in_progress","title":"x","timestamp":"2026-04-27T10:00:00Z"}'
  local sig; sig=$(sign "$body")
  cmd "curl -sSi -X POST $BASE_URL/webhook -H 'X-Signature-256: sha256=$sig' -d '$body'"
  curl -sSi -X POST "$BASE_URL/webhook" -H "Content-Type: application/json" -H "X-Signature-256: sha256=$sig" -d "$body"
  echo
}

s6() {
  bold "6) Webhook type errado (deleted)"
  local body='{"ticket_id":"CH-Z","type":"deleted","cpf":"'$CPF'","previous_status":"open","new_status":"in_progress","title":"x","timestamp":"2026-04-27T10:00:00Z"}'
  local sig; sig=$(sign "$body")
  cmd "curl -sSi -X POST $BASE_URL/webhook -H 'X-Signature-256: sha256=$sig' -d '$body'"
  curl -sSi -X POST "$BASE_URL/webhook" -H "Content-Type: application/json" -H "X-Signature-256: sha256=$sig" -d "$body"
  echo
}

s7() {
  bold "7) Webhook timestamp inválido"
  local body='{"ticket_id":"CH-T","type":"status_change","cpf":"'$CPF'","previous_status":"open","new_status":"in_progress","title":"x","timestamp":"ontem"}'
  local sig; sig=$(sign "$body")
  cmd "curl -sSi -X POST $BASE_URL/webhook -H 'X-Signature-256: sha256=$sig' -d '$body'"
  curl -sSi -X POST "$BASE_URL/webhook" -H "Content-Type: application/json" -H "X-Signature-256: sha256=$sig" -d "$body"
  echo
}

s8() {
  bold "8) GET /notifications sem Authorization"
  cmd "curl -sSi $BASE_URL/notifications"
  curl -sSi "$BASE_URL/notifications"
  echo
}

s9() {
  bold "9) GET /notifications com token"
  ensure_token
  cmd "curl -sSi -H 'Authorization: Bearer \$TOKEN' $BASE_URL/notifications"
  curl -sSi -H "Authorization: Bearer $TOKEN" "$BASE_URL/notifications"
  echo
}

s10() {
  bold "10) Paginação: limit=500 (deve cap em 100)"
  ensure_token
  cmd "curl -sSi -H 'Authorization: Bearer \$TOKEN' '$BASE_URL/notifications?limit=500'"
  curl -sSi -H "Authorization: Bearer $TOKEN" "$BASE_URL/notifications?limit=500"
  echo
}

s11() {
  bold "11) GET /notifications/unread-count"
  ensure_token
  cmd "curl -sSi -H 'Authorization: Bearer \$TOKEN' $BASE_URL/notifications/unread-count"
  curl -sSi -H "Authorization: Bearer $TOKEN" "$BASE_URL/notifications/unread-count"
  echo
}

s12() {
  bold "12) PATCH /notifications/:id/read (primeira da lista)"
  ensure_token
  local id; id=$(curl -s -H "Authorization: Bearer $TOKEN" "$BASE_URL/notifications" | python3 -c "import sys,json;print(json.load(sys.stdin)['items'][0]['id'])")
  cmd "curl -sSi -X PATCH -H 'Authorization: Bearer \$TOKEN' $BASE_URL/notifications/$id/read"
  curl -sSi -X PATCH -H "Authorization: Bearer $TOKEN" "$BASE_URL/notifications/$id/read"
  echo
}

s13() {
  bold "13) PATCH idempotente (mesma notificação 2x na mesma execução)"
  ensure_token
  local id; id=$(curl -s -H "Authorization: Bearer $TOKEN" "$BASE_URL/notifications" | python3 -c "import sys,json;print(json.load(sys.stdin)['items'][1]['id'])")
  cmd "curl -sSi -X PATCH -H 'Authorization: Bearer \$TOKEN' $BASE_URL/notifications/$id/read   # 1ª"
  curl -sSi -X PATCH -H "Authorization: Bearer $TOKEN" "$BASE_URL/notifications/$id/read"
  echo; echo
  cmd "curl -sSi -X PATCH -H 'Authorization: Bearer \$TOKEN' $BASE_URL/notifications/$id/read   # 2ª (já está lida)"
  curl -sSi -X PATCH -H "Authorization: Bearer $TOKEN" "$BASE_URL/notifications/$id/read"
  echo
}

s14() {
  bold "14) Isolamento entre cidadãos (PATCH em notif do outro)"
  ensure_token
  local id; id=$(curl -s -H "Authorization: Bearer $TOKEN" "$BASE_URL/notifications" | python3 -c "import sys,json;print(json.load(sys.stdin)['items'][0]['id'])")
  local outro; outro=$(token_for "$CPF_OUTRO")
  cmd "curl -sSi -X PATCH -H 'Authorization: Bearer \$TOKEN_OUTRO' $BASE_URL/notifications/$id/read"
  curl -sSi -X PATCH -H "Authorization: Bearer $outro" "$BASE_URL/notifications/$id/read"
  echo; echo
  cmd "curl -sSi -H 'Authorization: Bearer \$TOKEN_OUTRO' $BASE_URL/notifications   # lista do outro"
  curl -sSi -H "Authorization: Bearer $outro" "$BASE_URL/notifications"
  echo
}

s15() {
  bold "15) WebSocket: recepção em tempo real"
  command -v websocat >/dev/null || { echo "websocat não instalado. cargo install websocat"; return; }
  ensure_token
  local tid="CH-WS-$(date +%s)-$RANDOM"
  local body='{"ticket_id":"'"$tid"'","type":"status_change","cpf":"'"$CPF"'","previous_status":"in_progress","new_status":"completed","title":"WS test","timestamp":"'"$(date -u +%FT%TZ)"'"}'
  local sig; sig=$(sign "$body")

  printf "\033[2m[conectando websocat em background...]\033[0m\n"
  cmd "websocat -n ws://localhost:8081/ws -H 'Authorization: Bearer \$TOKEN'"
  timeout 60 websocat -n "ws://localhost:8081/ws" -H "Authorization: Bearer $TOKEN" &
  local ws_pid=$!
  sleep 3

  printf "\033[2m[disparando webhook após 3s]\033[0m\n"
  cmd "curl -sSi -X POST $BASE_URL/webhook -H 'X-Signature-256: sha256=$sig' -d '$body'"
  curl -sSi -X POST "$BASE_URL/webhook" -H "Content-Type: application/json" -H "X-Signature-256: sha256=$sig" -d "$body"
  echo

  printf "\033[2m[aguardando mensagem WS (até 60s; Ctrl-C pra cortar antes)...]\033[0m\n"
  wait $ws_pid
  echo
}

s16() {
  bold "16) WebSocket sem auth"
  cmd "curl -sSi -H 'Connection: Upgrade' -H 'Upgrade: websocket' -H 'Sec-WebSocket-Version: 13' -H 'Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==' $BASE_URL/ws"
  curl -sSi -H "Connection: Upgrade" -H "Upgrade: websocket" \
    -H "Sec-WebSocket-Version: 13" -H "Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==" \
    "$BASE_URL/ws"
  echo
  if command -v websocat >/dev/null; then
    cmd "websocat -v ws://localhost:8081/ws"
    timeout 3 websocat -v "ws://localhost:8081/ws"
    echo "exit=$?"
  else
    echo "(websocat não instalado — pulando o teste com cliente WS)"
  fi
  echo
}

s17() {
  bold "17) Privacidade: CPF não em claro no banco"
  cmd "psql -c \"SELECT id, ticket_id, encode(citizen_ref,'hex') FROM notifications LIMIT 3;\""
  docker exec desafio-prefeitura-rio-backend-postgres-1 \
    psql -U app -d notifications -c "SELECT id, ticket_id, encode(citizen_ref,'hex') AS citizen_ref FROM notifications LIMIT 3;"
  echo
}

s18() {
  bold "18) DLQ (invasivo: derruba postgres)"
  local n="${DLQ_N:-10}"
  echo "vai enfileirar $n webhooks com postgres down. ENTER pra continuar, Ctrl-C pra abortar"; read -r
  cmd "docker compose stop postgres"
  docker compose stop postgres 2>&1 | tail -3
  echo
  printf "\033[2m[disparando %d webhooks...]\033[0m\n" "$n"
  for i in $(seq 1 "$n"); do
    local body='{"ticket_id":"CH-DLQ-'"$(date +%s)-$i-$RANDOM"'","type":"status_change","cpf":"'$CPF'","previous_status":"open","new_status":"in_progress","title":"dlq '"$i"'","timestamp":"'$(date -u +%FT%TZ)'"}'
    local sig; sig=$(sign "$body")
    local code; code=$(curl -s -o /dev/null -w "%{http_code}" -X POST "$BASE_URL/webhook" \
      -H "Content-Type: application/json" -H "X-Signature-256: sha256=$sig" -d "$body")
    printf "  [%d/%d] -> %s\n" "$i" "$n" "$code"
  done
  echo
  cmd "redis-cli ZCARD dlq:webhooks:retry  (3 amostras com 1s)"
  for _ in 1 2 3; do
    docker exec desafio-prefeitura-rio-backend-redis-1 redis-cli ZCARD dlq:webhooks:retry
    sleep 1
  done
  echo
  cmd "docker compose start postgres   # worker drena em até 60s"
  docker compose start postgres 2>&1 | tail -3
  for i in $(seq 1 60); do
    sleep 1
    local len; len=$(docker exec desafio-prefeitura-rio-backend-redis-1 redis-cli ZCARD dlq:webhooks:retry)
    if [ "$len" = "0" ]; then
      echo "DLQ drenada em ${i}s"
      cmd "redis-cli LLEN dlq:webhooks:dead"
      docker exec desafio-prefeitura-rio-backend-redis-1 redis-cli LLEN dlq:webhooks:dead
      return
    fi
  done
  echo "DLQ ainda com $len após 60s"
}

s19() {
  bold "19) WebSocket: rajada de N webhooks (N=\${WS_N:-5}, intervalo=\${WS_GAP:-0.5}s)"
  command -v websocat >/dev/null || { echo "websocat não instalado. cargo install websocat"; return; }
  ensure_token
  local n="${WS_N:-5}"
  local gap="${WS_GAP:-0.5}"
  local wait_after="${WS_WAIT:-3}"

  printf "\033[2m[conectando websocat em background...]\033[0m\n"
  cmd "websocat -n ws://localhost:8081/ws -H 'Authorization: Bearer \$TOKEN'"
  local total_secs=$(awk "BEGIN{print int($n*$gap + $wait_after + 5)}")
  timeout "$total_secs" websocat -n "ws://localhost:8081/ws" -H "Authorization: Bearer $TOKEN" &
  local ws_pid=$!
  sleep 2

  printf "\033[2m[disparando %d webhooks com %ss de intervalo]\033[0m\n" "$n" "$gap"
  for i in $(seq 1 "$n"); do
    local tid="CH-MULTI-$(date +%s)-$i-$RANDOM"
    local body='{"ticket_id":"'"$tid"'","type":"status_change","cpf":"'"$CPF"'","previous_status":"open","new_status":"in_progress","title":"msg '"$i"'","timestamp":"'"$(date -u +%FT%TZ)"'"}'
    local sig; sig=$(sign "$body")
    local code; code=$(curl -s -o /dev/null -w "%{http_code}" -X POST "$BASE_URL/webhook" \
      -H "Content-Type: application/json" -H "X-Signature-256: sha256=$sig" -d "$body")
    printf "  [%d/%d] %s -> %s\n" "$i" "$n" "$tid" "$code"
    sleep "$gap"
  done

  printf "\033[2m[aguardando últimas mensagens WS (%ss)...]\033[0m\n" "$wait_after"
  wait $ws_pid
  echo
}

# ---------- runner ----------

declare -A SCENARIOS=(
  [1]="webhook 201" [2]="webhook idempotência" [3]="assinatura inválida" [4]="sem assinatura"
  [5]="cpf inválido" [6]="type errado" [7]="timestamp inválido"
  [8]="REST sem token" [9]="GET notifications" [10]="paginação cap" [11]="unread-count"
  [12]="mark read" [13]="mark read idempotente" [14]="isolamento entre cidadãos"
  [15]="WS recepção" [16]="WS sem auth"
  [17]="privacidade CPF" [18]="DLQ (invasivo)" [19]="WS rajada (WS_N, WS_GAP, WS_WAIT)"
)

run() {
  case "$1" in
    1) s1;; 2) s2;; 3) s3;; 4) s4;; 5) s5;; 6) s6;; 7) s7;; 8) s8;; 9) s9;;
    10) s10;; 11) s11;; 12) s12;; 13) s13;; 14) s14;; 15) s15;; 16) s16;; 17) s17;; 18) s18;; 19) s19;;
    *) echo "cenário inválido: $1";;
  esac
}

if [ "${1:-}" = "all" ]; then
  for n in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 17; do run "$n"; done
  exit 0
fi
if [ -n "${1:-}" ]; then run "$1"; exit 0; fi

while true; do
  printf "\n\033[1m=== cenários ===\033[0m\n"
  for n in $(echo "${!SCENARIOS[@]}" | tr ' ' '\n' | sort -n); do
    printf "  %2d) %s\n" "$n" "${SCENARIOS[$n]}"
  done
  printf "   q) sair\nescolha: "
  read -r choice
  [ "$choice" = "q" ] && break
  run "$choice"
done
