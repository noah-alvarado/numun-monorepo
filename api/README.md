# /api — NUMUN Lambdalith

Go backend served at `api.numun.org`. One Lambda, one binary, multiple commands
(`/cmd/api`, `/cmd/email-worker`, `/cmd/email-feedback`, `/cmd/cognito-post-confirmation`
land per milestone).

## First-time setup

```sh
# 1. Generate Connect/Protobuf code from /api/proto into /api/internal/gen
make proto

# 2. Resolve Go dependencies
cd api && go mod tidy

# 3. Build (sanity check)
cd api && go build ./...
```

## Local run (outside SAM)

```sh
cd api && LOCAL_HTTP=true go run ./cmd/api
curl http://localhost:3000/v1/health
```

## Local run (inside SAM Local; the prod-shaped path)

```sh
make dev          # brings up docker-compose
make dev-api      # SAM Local API Gateway → Lambdalith on :3000
```

See APPLICATION.md §4 and the root IMPLEMENTATION_PLAN.md for milestone scope.
