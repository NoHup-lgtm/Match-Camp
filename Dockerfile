FROM golang:1.26-alpine AS build

WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/matchcamp ./cmd/api

FROM alpine:3.22

RUN adduser -D -H matchcamp
USER matchcamp
WORKDIR /app
COPY --from=build /out/matchcamp /app/matchcamp

EXPOSE 8080
ENTRYPOINT ["/app/matchcamp"]
