# CNCacheHub Server

CNCacheHub 的 Go HTTP 服务端（Phase 0 骨架）。

完整产品需求见仓库根 `docs/prd.md`；项目规则见 `AGENTS.md`。

## 快速开始

```bash
# 直接跑（默认 :8080，data 写到 ./data）
make run

# 或显式指定环境变量
CNCH_HTTP_ADDR=:8080 \
CNCH_DATA_DIR=./data \
CNCH_LOG_LEVEL=info \
go run ./cmd/cncachehub
```

启动后访问：

```bash
curl http://localhost:8080/healthz       # {"status":"ok"}
curl http://localhost:8080/api/healthz   # {"status":"ok","uptime":"...","db":"ok","version":"dev"}
curl http://localhost:8080/api/version   # {"name":"cncachehub","version":"dev","go":"go1.22.x","commit":"local"}
```

## 端点列表（Phase 0）

| Method | Path           | 用途                              |
|--------|----------------|-----------------------------------|
| GET    | `/healthz`     | 轻量 liveness，不访问依赖         |
| GET    | `/api/healthz` | 完整 readiness，含 uptime / db    |
| GET    | `/api/version` | 构建信息（name / version / go / commit） |

所有响应统一 `application/json`；错误响应统一：

```json
{ "error": { "code": "...", "message": "..." } }
```

## 环境变量

| 变量                                | 默认值      | 说明                          |
|-------------------------------------|-------------|-------------------------------|
| `CNCH_HTTP_ADDR`                    | `:8080`     | HTTP 监听地址（host:port）    |
| `CNCH_DATA_DIR`                     | `./data`    | 数据目录，DB 在 `${DATA_DIR}/cncachehub.db` |
| `CNCH_LOG_LEVEL`                    | `info`      | debug / info / warn / error   |
| `CNCH_ADMIN_PASSWORD`               | （空）      | 控制台管理员密码；空 = 未配置 |
| `CNCH_SHUTDOWN_TIMEOUT_SECONDS`     | `30`        | Graceful shutdown 超时（秒）  |

启动时日志会打印生效配置；`admin_password` 字段会被自动脱敏为 `***`（其他敏感字段
如 `token` / `secret` / `api_key` / `cookie` / `authorization` 同样脱敏）。

## 构建

```bash
# 本地默认平台
make build
./bin/cncachehub

# 交叉编译（纯 Go，无 CGO）
GOOS=linux  GOARCH=amd64 make build
GOOS=linux  GOARCH=arm64 make build
GOOS=darwin GOARCH=arm64 make build
```

## 测试

```bash
make test    # = go test ./... -race -v
make lint    # = go vet ./...
```

## Docker

```bash
docker build -t cncachehub:dev -f server/Dockerfile server
docker run --rm -p 8080:8080 \
  -v $(pwd)/server/data:/var/lib/cncachehub/data \
  cncachehub:dev
```

镜像用 `golang:1.22-alpine` 编译 → `alpine:3.19` 运行；非 root 用户（uid 10001）；
内置 `HEALTHCHECK` 走 `/healthz`。

## 目录结构

```
server/
├── cmd/cncachehub/main.go        # 入口
├── internal/
│   ├── config/                   # 环境变量加载与校验
│   ├── log/                      # slog JSON + 敏感字段脱敏
│   ├── storage/                  # SQLite 封装 + migrations runner
│   │   └── migrations/0001_init.sql
│   └── api/                      # chi 路由 + middleware + handlers
├── go.mod
├── go.sum
├── Makefile
├── Dockerfile
└── README.md
```
