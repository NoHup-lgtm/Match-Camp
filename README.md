# Matchcamp

Backend/infra MVP para um app de matches da faculdade.

## Stack

- Go: API REST e WebSocket.
- PostgreSQL: fonte da verdade.
- Redis: presenca e fanout em tempo real.
- Docker Compose: ambiente local.
- goose: migrations.
- sqlc: configurado para queries tipadas, ainda sem queries geradas no primeiro corte.

## Bootstrap

```sh
cp .env.example .env
go mod tidy
go test ./...
docker compose up --build
```

Em outra aba, aplicar migrations:

```sh
go install github.com/pressly/goose/v3/cmd/goose@latest
goose -dir migrations postgres "postgres://matchcamp:matchcamp@localhost:5432/matchcamp?sslmode=disable" up
```

## Endpoints

- `GET /health`
- `POST /v1/auth/register`
- `POST /v1/auth/login`
- `GET /v1/auth/google/start`
- `GET /v1/auth/google/callback`
- `POST /v1/auth/logout`
- `GET /v1/me`
- `PUT /v1/profile`
- `PATCH /v1/profile/visibility`
- `GET /v1/discovery`
- `POST /v1/swipes`
- `GET /v1/matches`
- `GET /v1/conversations`
- `GET /v1/conversations/{id}/messages`
- `POST /v1/conversations/{id}/messages`
- `GET /v1/ws`

## Chat

Chat e somente texto.

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
- Docker: namespaces, cgroups, bridge network, volumes.
