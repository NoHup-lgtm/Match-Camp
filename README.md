# Matchcamp — Backend

API REST + WebSocket para um app de matches universitário.

## Stack

| Camada | Tecnologia |
|--------|-----------|
| API | Go + chi |
| Banco | PostgreSQL + pgx + sqlc |
| Cache / RT | Redis (pub/sub + presença) |
| Migrations | goose |
| Storage | Cloudflare R2 (prod) / local (dev) |
| Container local | Podman + podman-compose |

## Endpoints

Documentação completa em `/docs` (Swagger UI) ou `/openapi.yaml`.

| Método | Rota | Descrição |
|--------|------|-----------|
| GET | /health | Status do banco e Redis |
| POST | /v1/auth/register | Cadastro com email/senha |
| POST | /v1/auth/login | Login |
| POST | /v1/auth/logout | Logout |
| GET | /v1/auth/google/start | OAuth Google |
| GET | /v1/me | Perfil do usuário autenticado |
| PUT | /v1/profile | Salvar perfil |
| PATCH | /v1/profile/visibility | Ativar/desativar no discovery |
| PUT | /v1/profile/preferences | Preferências (faixa de idade) |
| PUT/DELETE | /v1/profile/photos/{0-3} | Upload e remoção de foto |
| GET | /v1/discovery | Perfis para swipe |
| POST | /v1/swipes | Like ou pass |
| GET | /v1/matches | Lista de matches |
| GET | /v1/users/{id} | Perfil público de outro usuário |
| POST/DELETE | /v1/users/{id}/block | Bloquear / desbloquear |
| GET | /v1/conversations | Lista de conversas |
| GET/POST | /v1/conversations/{id}/messages | Mensagens (paginado) |
| GET | /v1/ws | WebSocket em tempo real |

## Rodar localmente

### Com Podman (recomendado)

```sh
brew install podman podman-compose  # instalar uma vez
podman machine init && podman machine start

cp .env.example .env
podman compose up --build
```

A API sobe em `http://localhost:8080`. Migrations rodam automaticamente pelo entrypoint.

> **Seed de dados:** `go run ./cmd/seed` cria 10 usuários com perfil completo (senha: `senha1234`).

### Sem container

```sh
# PostgreSQL e Redis precisam estar rodando
cp .env.example .env  # ajuste DATABASE_URL e REDIS_URL
make migrate-up
make dev
```

## Deploy (grátis para MVP)

| Serviço | Uso |
|---------|-----|
| [Fly.io](https://fly.io) | API Go |
| [Supabase](https://supabase.com) | PostgreSQL |
| [Upstash](https://upstash.com) | Redis |
| [Cloudflare R2](https://cloudflare.com/r2) | Fotos de perfil |

### Passo a passo

**1. Supabase — criar projeto e pegar connection string**
- Novo projeto em `supabase.com`
- Settings → Database → Transaction Pooler → copie a URI
- Salve como `DATABASE_URL` no Fly

**2. Upstash — criar banco Redis**
- Novo banco em `upstash.com` (região São Paulo)
- Copie a URL `rediss://...`
- Salve como `REDIS_URL` + ative `REDIS_TLS=true` no Fly

**3. Cloudflare R2 — criar bucket**
- Já configurado no código; preencha `R2_*` vars no Fly

**4. Fly.io — deploy da API**

```sh
brew install flyctl
flyctl auth login
flyctl launch --name matchcamp-api --region gru --no-deploy
```

Setar segredos:
```sh
flyctl secrets set \
  DATABASE_URL="postgres://..." \
  REDIS_URL="rediss://..." \
  REDIS_TLS="true" \
  SESSION_COOKIE_SECURE="true" \
  ALLOWED_ORIGINS="https://SEU_FRONTEND.com" \
  R2_ENDPOINT="..." \
  R2_BUCKET="..." \
  R2_ACCESS_KEY_ID="..." \
  R2_SECRET_ACCESS_KEY="..." \
  R2_PUBLIC_BASE_URL="..."
```

Deploy:
```sh
flyctl deploy
```

**5. Migrations em produção**

```sh
flyctl ssh console
cd /app && goose -dir migrations postgres "$DATABASE_URL" up
```

**6. CI/CD automático**

Adicione o token do Fly como secret no GitHub:
- `flyctl tokens create deploy` → copie o token
- GitHub repo → Settings → Secrets → `FLY_API_TOKEN`

A partir daí todo push para `main` faz deploy automático via `.github/workflows/deploy.yml`.

## WebSocket — eventos

| Tipo | Direção | Payload |
|------|---------|---------|
| `message` | server → client | `{type, id, conversation_id, sender_user_id, text, is_read, created_at}` |
| `match` | server → client | `{type, match_id, conversation_id, partner: {id, display_name, photo_url}}` |
| `typing` | client → server | `{type: "typing", conversation_id}` |
| `typing` | server → client | `{type: "typing", conversation_id, user_id}` |

## Erros

Todos os erros seguem o mesmo envelope:

```json
{
  "error": {
    "code": "invalid_credentials",
    "message": "Email ou senha invalidos.",
    "request_id": "4bdfb6c2d46d/BtJz9SRWbG-000001"
  }
}
```
