#!/usr/bin/env bash
# secrets-from-vaultwarden.sh
#
# Скрипт-helper для PKI-9: пересоздаёт k8s Secret pki-api-env (и набор
# зависимостных secret'ов postgres/redis/rabbitmq/minio) из Vaultwarden.
#
# ПРЕДУСЛОВИЯ:
#   1. brew install bitwarden-cli         # для CLI bw
#   2. bw login --apikey                  # один раз, нужны BW_CLIENTID/BW_CLIENTSECRET
#      (из Vaultwarden → Account Settings → Security → Keys → API key)
#   3. export BW_SESSION=$(bw unlock --raw)   # сессионный токен
#   4. export KUBECONFIG=~/.kube/fin4b-dev-k8s-01.yaml
#
# Структура записи в Vaultwarden (folder platform-pki/prod):
#   item "pki-api-env" (Login → Custom Fields):
#     APP_JWT_SECRET, DATABASE_DSN, STORAGE_ACCESS_KEY, STORAGE_SECRET_KEY,
#     REDIS_PASSWORD, RABBITMQ_URL
#   item "deps" (Login → Custom Fields):
#     PG_USER, PG_PASS, PG_DB, REDIS_PASS, RMQ_USER, RMQ_PASS,
#     MINIO_ACCESS, MINIO_SECRET
#
# Использование:
#   ./secrets-from-vaultwarden.sh apply
#       — пересоздаёт Secret'ы в namespace platform
#   ./secrets-from-vaultwarden.sh diff
#       — показывает что изменится (kubectl diff)

set -euo pipefail

NS=${NS:-platform}
ACTION=${1:-apply}

if ! command -v bw >/dev/null; then
  echo "FATAL: bw CLI не найдено. brew install bitwarden-cli" >&2
  exit 1
fi
if [ -z "${BW_SESSION:-}" ]; then
  echo "FATAL: BW_SESSION пустая. Сначала: export BW_SESSION=\$(bw unlock --raw)" >&2
  exit 1
fi
bw sync >/dev/null

# helper: достать значение custom field из bw item
bw_field() {  # bw_field <item_name> <field_name>
  bw get item "$1" 2>/dev/null \
    | python3 -c "
import json,sys
d=json.load(sys.stdin)
for f in (d.get('fields') or []):
  if f['name']=='$2': print(f['value']); break
"
}

# === читаем все нужные значения ===
APP_JWT_SECRET=$(bw_field pki-api-env APP_JWT_SECRET)
DATABASE_DSN=$(bw_field pki-api-env DATABASE_DSN)
STORAGE_ACCESS_KEY=$(bw_field pki-api-env STORAGE_ACCESS_KEY)
STORAGE_SECRET_KEY=$(bw_field pki-api-env STORAGE_SECRET_KEY)
REDIS_PASSWORD=$(bw_field pki-api-env REDIS_PASSWORD)
RABBITMQ_URL=$(bw_field pki-api-env RABBITMQ_URL)

PG_USER=$(bw_field deps PG_USER)
PG_PASS=$(bw_field deps PG_PASS)
PG_DB=$(bw_field deps PG_DB)
REDIS_PASS=$(bw_field deps REDIS_PASS)
RMQ_USER=$(bw_field deps RMQ_USER)
RMQ_PASS=$(bw_field deps RMQ_PASS)
MINIO_ACCESS=$(bw_field deps MINIO_ACCESS)
MINIO_SECRET=$(bw_field deps MINIO_SECRET)

# Sanity-check: все обязательные значения непусты
for v in APP_JWT_SECRET DATABASE_DSN STORAGE_ACCESS_KEY STORAGE_SECRET_KEY \
         REDIS_PASSWORD RABBITMQ_URL PG_USER PG_PASS PG_DB REDIS_PASS \
         RMQ_USER RMQ_PASS MINIO_ACCESS MINIO_SECRET; do
  if [ -z "${!v:-}" ]; then echo "FATAL: $v пуст в Vaultwarden" >&2; exit 1; fi
done

# === собираем yaml-документы ===
TMP=$(mktemp -d); trap "rm -rf $TMP" EXIT

cat >"$TMP/all.yaml" <<EOF
$(kubectl -n "$NS" create secret generic pki-api-env \
    --from-literal=APP_ENV=k8s \
    --from-literal=APP_PORT=8080 \
    --from-literal=APP_VERIFY_BASE_URL=https://pki.fin4b.kz \
    --from-literal=APP_JWT_SECRET="$APP_JWT_SECRET" \
    --from-literal=DATABASE_DSN="$DATABASE_DSN" \
    --from-literal=NCANODE_URL=http://ncanode.platform.svc:14579 \
    --from-literal=STORAGE_ENDPOINT=http://minio.platform.svc:9000 \
    --from-literal=STORAGE_REGION=us-east-1 \
    --from-literal=STORAGE_BUCKET=eds-prod \
    --from-literal=STORAGE_ACCESS_KEY="$STORAGE_ACCESS_KEY" \
    --from-literal=STORAGE_SECRET_KEY="$STORAGE_SECRET_KEY" \
    --from-literal=STORAGE_USE_PATH_STYLE=true \
    --from-literal=REDIS_ADDR=redis.platform.svc:6379 \
    --from-literal=REDIS_PASSWORD="$REDIS_PASSWORD" \
    --from-literal=RABBITMQ_URL="$RABBITMQ_URL" \
    --from-literal=LOG_LEVEL=info \
    --from-literal=LOG_FORMAT=json \
    --dry-run=client -o yaml)
---
$(kubectl -n "$NS" create secret generic postgres-secrets \
    --from-literal=user="$PG_USER" \
    --from-literal=password="$PG_PASS" \
    --from-literal=db="$PG_DB" \
    --dry-run=client -o yaml)
---
$(kubectl -n "$NS" create secret generic redis-secrets \
    --from-literal=password="$REDIS_PASS" \
    --dry-run=client -o yaml)
---
$(kubectl -n "$NS" create secret generic rabbitmq-secrets \
    --from-literal=user="$RMQ_USER" \
    --from-literal=password="$RMQ_PASS" \
    --dry-run=client -o yaml)
---
$(kubectl -n "$NS" create secret generic minio-secrets \
    --from-literal=access-key="$MINIO_ACCESS" \
    --from-literal=secret-key="$MINIO_SECRET" \
    --dry-run=client -o yaml)
EOF

case "$ACTION" in
  diff)  kubectl diff -f "$TMP/all.yaml" || true ;;
  apply) kubectl apply -f "$TMP/all.yaml" ;;
  *)     echo "usage: $0 [apply|diff]" >&2; exit 2 ;;
esac
