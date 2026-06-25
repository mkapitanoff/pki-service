# PKI Service — Deploy

Манифесты для деплоя `pki-api` в k3s `fin4b-dev-k8s-01`, namespace `platform`.
Структура — по аналогии с `core/iam`.

## Файлы

```
deploy/
├── k8s/
│   ├── 00-config.yaml   # ConfigMap pki-api-config + Secret pki-secrets (шаблон)
│   └── pki.yaml         # Deployment + Service + Ingress
└── argocd/
    ├── 01-repo-secret.yaml      # Argo CD repository credential для core/pki
    └── 02-app-platform.yaml     # Application platform-pki
```

## Зависимости (поднимаются отдельным спринтом, PKI-10)

`pki-api` в k8s требует cluster-internal сервисы:

| Сервис | DNS |
|---|---|
| PostgreSQL 15 | `postgres.platform.svc:5432` |
| Redis 7 | `redis.platform.svc:6379` |
| RabbitMQ 3.12 | `rabbitmq.platform.svc:5672` |
| MinIO | `minio.platform.svc:9000` |
| NCANode 3 | `ncanode.platform.svc:14579` |

Пока их нет — pod упадёт на старте (`/health` ожидает доступности БД). Это нормально.

## Образ

Сборка и импорт — по PKI-7 (без registry, через `k3s ctr images import`):

```sh
SHA=$(git rev-parse --short HEAD)
docker build --platform linux/amd64 -f docker/Dockerfile -t pki-api:$SHA -t pki-api:latest .
docker save pki-api:$SHA pki-api:latest -o /tmp/pki-api.tar
scp -i ~/.ssh/fin4b_pki_deploy /tmp/pki-api.tar pki-deploy@10.207.22.13:/tmp/
ssh -i ~/.ssh/fin4b_pki_deploy pki-deploy@10.207.22.13 'sudo k3s ctr images import /tmp/pki-api.tar && rm /tmp/pki-api.tar'
```

Deployment всегда тянет `pki-api:latest`. После CI/Registry (PKI-22) тег станет
`registry.gitlab.fin4b.kz/core/pki:<sha>` и `imagePullPolicy: Never` уйдёт.

## Конфиг

Контейнер запускается с `APP_ENV=k8s` → Viper читает
`/app/configs/config.k8s.yaml`, который смонтирован из ConfigMap
`pki-api-config` (см. `k8s/00-config.yaml`).

Чувствительные значения (DSN с паролем, JWT secret, S3 access/secret и т.д.)
сейчас в ConfigMap как placeholder'ы `CHANGE_ME_*`. **До деплоя нужно** либо:

1. **Быстрый путь, лифт-и-шифт**: подставить реальные значения прямо в
   `00-config.yaml` через kustomize-patch или `sed` перед `kubectl apply`
   (не коммитить!).
2. **Правильный путь (PKI-9 + мини-PR в коде)**: добавить `BindEnv` для
   nested ключей в `internal/config/config.go`, переключить Deployment на
   `envFrom: secretRef: pki-secrets`, реальные значения держать в Secret из
   Vaultwarden. Шаблон Secret уже лежит в `00-config.yaml`.

## Ручной деплой (без Argo CD)

```sh
# namespace уже должен существовать
kubectl get ns platform || kubectl create ns platform

# конфиг + Deployment + Service + Ingress
kubectl apply -f deploy/k8s/

# (опц.) подменить Secret реальными значениями из Vaultwarden
kubectl -n platform create secret generic pki-secrets \
  --from-literal=DATABASE_DSN='...' \
  --from-literal=APP_JWT_SECRET='...' \
  ...

# проверки
kubectl -n platform get pods -l app=pki-api
kubectl -n platform port-forward svc/pki-api 8080:8080
curl http://localhost:8080/health
```

## Через Argo CD (PKI-13)

```sh
# repository credential
kubectl -n argocd apply -f deploy/argocd/01-repo-secret.yaml   # подставить токен!

# Application
kubectl apply -f deploy/argocd/02-app-platform.yaml
```

Argo CD дальше сам подтянет `deploy/k8s/` и применит. `syncPolicy: automated`
+ `selfHeal: true` — при ручных правках в кластере вернёт состояние из git.

## ALB / DNS

Внешний доступ — `pki.fin4b.kz`:

- Web Route в ICDC ALB (PKI-14): `pki.fin4b.kz → fin4b-dev-k8s-01:80`, TLS
  терминируется на edge.
- DNS A `pki.fin4b.kz → 185.206.34.145` (PKI-15).

Сам Ingress в кластере слушает только HTTP — TLS делает ALB.
