# Build context: repo root. One image runs api, worker, seed (spec §2).
FROM golang:1.26-alpine AS build
WORKDIR /src/services/api
COPY services/api/go.mod services/api/go.sum ./
RUN go mod download
COPY services/api ./
RUN CGO_ENABLED=0 go build -o /out/ ./cmd/...

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata
COPY --from=build /out/ /usr/local/bin/
COPY db/migrations /app/db/migrations
WORKDIR /app
ENV MIGRATIONS_DIR=/app/db/migrations
CMD ["api"]
