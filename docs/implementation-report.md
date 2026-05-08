# Matchcamp Backend - Relatorio de Implementacao

Data: 2026-05-08

## Escopo

Este documento registra o que foi implementado no backend/infra do Matchcamp ate
este ponto, os bloqueios encontrados e o estado tecnico atual do projeto.

O escopo continua restrito a backend, infraestrutura local, banco de dados e
contratos de API. Interface web/mobile fica fora deste repositorio.

## Stack Atual

- Go para API HTTP REST e WebSocket.
- PostgreSQL como fonte da verdade.
- Redis para presenca e fanout de eventos em tempo real.
- pgx com sqlc para acesso SQL tipado.
- goose para migrations.
- Cloudflare R2 como object storage planejado para producao.
- Driver local de storage mantido para desenvolvimento.
- Docker Compose mantido no repositorio, mas nao e mais requisito unico para rodar local.

## O Que Ja Foi Implementado

### Estrutura do Projeto

- Projeto Go inicializado como backend modular.
- Estrutura principal:
  - `cmd/api`
  - `internal/auth`
  - `internal/config`
  - `internal/database`
  - `internal/server`
  - `internal/storage`
  - `internal/apperror`
  - `migrations`
  - `queries`
  - `docs`
  - `postman`

### Banco de Dados

- Migrations principais criadas.
- Tabelas principais:
  - `users`
  - `auth_identities`
  - `sessions`
  - `profiles`
  - `profile_photos`
  - `profile_preferences`
  - `swipes`
  - `matches`
  - `conversations`
  - `conversation_members`
  - `messages`
- Regras importantes:
  - `messages.body` e somente texto.
  - Chat nao tem tabela de anexos.
  - Fotos de perfil limitadas a posicoes `0..3`.
  - Swipe tem unicidade por ator/alvo.
  - Match usa par normalizado para evitar duplicidade.

### Autenticacao

- Cadastro por email/senha.
- Login por email/senha.
- Logout.
- Sessao por token opaco em cookie `HttpOnly`.
- Hash de token de sessao salvo no banco.
- Base para Google OAuth implementada por variaveis de ambiente.

### Perfil

- Endpoint de usuario atual.
- Criacao/atualizacao de perfil.
- Controle de visibilidade.
- Perfil so entra em descoberta quando esta completo e visivel.

### Fotos de Perfil

- Cada usuario pode cadastrar ate 4 fotos.
- Upload por posicao: `0`, `1`, `2`, `3`.
- Tipos aceitos: JPEG, PNG e WebP.
- Limite padrao: 5 MiB por foto.
- Substituir foto remove o objeto antigo quando possivel.
- Deletar foto remove registro do banco e tenta remover objeto do storage.

### Matching

- Discovery exclui:
  - proprio usuario
  - usuario invisivel
  - usuario ja avaliado
- Swipe aceita `like` e `pass`.
- Like reciproco cria match em transacao.
- Conversa e criada a partir do match.

### Chat

- Chat e texto-only.
- Sem anexos, imagens, videos, audio ou upload.
- Payload com campos extras e rejeitado.
- Links `http://`, `https://` e `data:` sao rejeitados no texto.
- Mensagem e persistida no PostgreSQL antes de publicar evento.
- Redis Pub/Sub e usado para fanout em tempo real.
- WebSocket fica em `/v1/ws`.

### Erros Padronizados

- Criado catalogo central em `internal/apperror`.
- Resposta de erro padrao:

```json
{
  "error": {
    "code": "invalid_credentials",
    "message": "Email ou senha invalidos.",
    "request_id": "..."
  }
}
```

- `code` continua estavel em ingles para web/mobile.
- `message` e publica em portugues.
- `request_id` vem do middleware HTTP e ajuda a correlacionar logs.

### OpenAPI

- Spec criada em `docs/openapi.yaml`.
- Endpoints expostos:
  - `GET /openapi.yaml`
  - `GET /docs`
- Swagger UI aponta para a spec local.
- A spec cobre auth, perfil, fotos, discovery, swipes, matches, conversas,
  mensagens e WebSocket.

### Cloudflare R2

- Criada interface interna de storage:
  - `Put`
  - `Delete`
  - `KeyFromURL`
- Driver local:
  - usado em dev com `STORAGE_DRIVER=local`
  - salva em `UPLOAD_DIR`
- Driver R2:
  - usado com `STORAGE_DRIVER=r2`
  - usa AWS SDK v2 com endpoint S3-compatible
  - salva objetos no formato:

```txt
profile-photos/{user_id}/{position}-{random}.{ext}
```

- Banco continua salvando apenas a URL publica em `profile_photos.url`.

### Documentacao e Postman

- README atualizado com:
  - stack
  - endpoints
  - fotos
  - erros padronizados
  - alternativa sem Docker/Podman
- Collection Postman atualizada com:
  - `/docs`
  - `/openapi.yaml`

## Impeditivos Encontrados

### Docker

O Docker foi removido da maquina e nao esta mais disponivel no `PATH`.
Por isso, nao foi possivel executar:

```sh
docker compose up --build
```

Impacto:

- Nao houve smoke test local via Compose nesta entrega.
- Nao foi possivel subir Postgres/Redis em container nesta maquina.
- Validacao com banco real depende de Postgres/Redis nativos ou servicos gerenciados.

### Podman

Podman foi considerado como substituto, mas o ambiente local ja apresentou falhas
anteriores de VM/socket e nao esta confiavel.

Impacto:

- Nao vale gastar tempo tentando mascarar problema de runtime.
- O caminho pragmatico e rodar Postgres/Redis via Homebrew ou usar servicos
  gerenciados/free tier.

### Redis Gerenciado

O backend hoje espera Redis simples por `REDIS_ADDR`, sem senha e sem TLS.

Impacto:

- Para usar Upstash/Redis gerenciado em producao, o cliente Redis precisa ganhar
  configuracao de senha/TLS.

## Validacao Feita

Comandos executados com sucesso:

```sh
go mod tidy
go test ./...
go vet ./...
go build -o /private/tmp/matchcamp-api ./cmd/api
jq empty postman/Matchcamp.postman_collection.json
```

Validacao nao feita por bloqueio de runtime:

```sh
docker compose up --build
goose -dir migrations postgres "$DATABASE_URL" up
smoke real com Postgres/Redis locais
```

## Como Rodar Sem Docker/Podman

Instalar Postgres e Redis nativos ou usar servicos externos.

Exemplo local com Homebrew:

```sh
brew install postgresql@18 redis
brew services start postgresql@18
brew services start redis
psql postgres -c "create user matchcamp with password 'matchcamp';"
createdb -O matchcamp matchcamp
goose -dir migrations postgres "postgres://matchcamp:matchcamp@localhost:5432/matchcamp?sslmode=disable" up
DATABASE_URL="postgres://matchcamp:matchcamp@localhost:5432/matchcamp?sslmode=disable" REDIS_ADDR="localhost:6379" make dev
```

## Proximos Passos Tecnicos

Prioridade alta:

- Rodar smoke real com Postgres/Redis fora de container.
- Adicionar suporte a Redis com senha/TLS.
- Implementar CSRF/CORS corretamente para web usando cookie.
- Adicionar rate limit em login, cadastro, swipe e chat.
- Melhorar WebSocket com ping/pong e controle de escrita concorrente.

Prioridade media:

- Paginar discovery, matches, conversas e mensagens.
- Criar seeds para testar frontend/mobile.
- Adicionar CI no GitHub Actions.
- Validar OpenAPI em pipeline.
- Preparar deploy em free tier.

Prioridade baixa:

- Melhorar observabilidade com logs estruturados.
- Adicionar metricas basicas.
- Preparar politica de retencao de sessoes e mensagens.

## Decisoes De Produto Mantidas

- Chat segue texto-only.
- Foto de perfil e permitida, com limite de 4 fotos por usuario.
- Nenhum anexo sera aceito no chat.
- Nenhum storage de midia sera criado para mensagens.
- PostgreSQL continua sendo a fonte da verdade.
- Redis nao garante historico; historico vem sempre do PostgreSQL.

