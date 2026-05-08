# Matchcamp

Backend/infra MVP para um app de matches da faculdade.

## Stack

- Go: API REST e WebSocket.
- PostgreSQL: fonte da verdade.
- Redis: presenca e fanout em tempo real.
- Docker Compose: ambiente local quando existir runtime de container.
- goose: migrations.
- sqlc: queries SQL gerando codigo Go tipado.
- Cloudflare R2: object storage de producao para fotos de perfil.

## Bootstrap

Com Docker/Podman funcionando:

```sh
cp .env.example .env
go mod tidy
sqlc generate
go test ./...
docker compose up --build
```

Em outra aba, aplicar migrations:

```sh
go install github.com/pressly/goose/v3/cmd/goose@latest
goose -dir migrations postgres "postgres://matchcamp:matchcamp@localhost:5432/matchcamp?sslmode=disable" up
```

Sem Docker/Podman, rode PostgreSQL e Redis nativos ou use servicos gerenciados.
A API nao depende de container; ela depende de socket TCP aberto para Postgres e Redis.

Exemplo com Homebrew:

```sh
brew install postgresql@18 redis
brew services start postgresql@18
brew services start redis
psql postgres -c "create user matchcamp with password 'matchcamp';"
createdb -O matchcamp matchcamp
goose -dir migrations postgres "postgres://matchcamp:matchcamp@localhost:5432/matchcamp?sslmode=disable" up
DATABASE_URL="postgres://matchcamp:matchcamp@localhost:5432/matchcamp?sslmode=disable" REDIS_ADDR="localhost:6379" make dev
```

Se usar banco gerenciado, aponte `DATABASE_URL` para ele. Se usar Redis gerenciado,
aponte `REDIS_ADDR` para o host/porta e configure senha/TLS quando o backend passar
a suportar esses parametros. Hoje o Redis local esperado e simples: TCP em
`localhost:6379`, sem senha e sem TLS.

## Endpoints

- `GET /health`
- `GET /docs`
- `GET /openapi.yaml`
- `POST /v1/auth/register`
- `POST /v1/auth/login`
- `GET /v1/auth/google/start`
- `GET /v1/auth/google/callback`
- `POST /v1/auth/logout`
- `GET /v1/me`
- `PUT /v1/profile`
- `PATCH /v1/profile/visibility`
- `GET /v1/profile/photos`
- `PUT /v1/profile/photos/{position}`
- `DELETE /v1/profile/photos/{position}`
- `GET /v1/discovery`
- `POST /v1/swipes`
- `GET /v1/matches`
- `GET /v1/conversations`
- `GET /v1/conversations/{id}/messages`
- `POST /v1/conversations/{id}/messages`
- `GET /v1/ws`

## Chat

Chat e somente texto.

## Fotos De Perfil

Cada usuario pode cadastrar ate 4 fotos de perfil nas posicoes `0..3`.

Upload:

```sh
curl -X PUT \
  -b cookies.txt \
  -F "photo=@/caminho/foto.jpg" \
  http://localhost:8080/v1/profile/photos/0
```

Formatos aceitos: JPEG, PNG e WebP. O limite padrao e 5 MiB por foto.
Em dev, as fotos ficam no diretorio configurado por `UPLOAD_DIR` com `STORAGE_DRIVER=local`.
No Compose, esse diretorio usa o volume Docker `uploads-data`.
Em producao, use `STORAGE_DRIVER=r2` com Cloudflare R2.

## Erros

Erros seguem envelope padronizado:

```json
{
  "error": {
    "code": "invalid_credentials",
    "message": "Email ou senha invalidos.",
    "request_id": "..."
  }
}
```

Payload permitido:

```json
{
  "conversation_id": "00000000-0000-0000-0000-000000000000",
  "text": "mensagem"
}
```

Qualquer campo extra e rejeitado. Links `http://`, `https://` e `data:` tambem sao rejeitados.

## Fundamentos para estudar

- Go runtime: goroutines, scheduler M:N, stack crescente, escape analysis.
- HTTP: sockets TCP, keep-alive, headers, cookies HttpOnly.
- WebSocket: upgrade HTTP, conexao longa, file descriptors, backpressure.
- PostgreSQL: MVCC, transacoes, indices unicos, foreign keys, locks.
- Redis Pub/Sub: fanout sem persistencia e sem entrega para offline.
- Containers: namespaces, cgroups, bridge network, volumes. Sem runtime de container, o custo
  operacional vira instalar e manter Postgres/Redis diretamente no sistema ou consumir servicos gerenciados.
