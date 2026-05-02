# Matchcamp

Backend/infra MVP para um app de matches da faculdade.

## Stack

- Go: API REST e WebSocket.
- PostgreSQL: fonte da verdade.
- Redis: presenca e fanout em tempo real.
- Docker Compose: ambiente local.
- goose: migrations.
- sqlc: queries SQL gerando codigo Go tipado.

## Bootstrap

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
As fotos ficam no volume Docker `uploads-data` e sao servidas por `/uploads/profile-photos/{arquivo}`.

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
