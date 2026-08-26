# CNCacheHub Server

CNCacheHub 的 Go HTTP 服务端（v0.1.1 — Docker / SteamCMD / HF / 资源加速 / 鉴权 / 诊断全栈）。

完整产品需求见仓库根 `docs/prd.md`；项目规则见 `AGENTS.md`。
安全模型与建议见 `docs/security.md`；部署模式对比见 `docs/deploy-modes.md`。

## 快速开始

```bash
# 直接跑（默认 :8080，data 写到 ./data）
go run ./cmd/cncachehub

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
curl http://localhost:8080/api/version   # {"name":"cncachehub","version":"dev","go":"go1.23.x","commit":"local"}
```

## 端点列表

完整列表见 `internal/api/api.go`（chi 路由）。主要类别：

| 路径前缀 | 用途 | 鉴权 |
|---|---|---|
| `/healthz` `/api/healthz` `/api/version` | 健康检查 + 版本 | 公开 |
| `/api/auth/*` | 登录 / 登出 / 改密 / admin 管理 | 部分公开 + Cookie |
| `/v2/*` | Docker Hub pull-through 缓存（反代） | 公开（可加 access control） |
| `/r/*` | 资源加速（GitHub / HF / Playwright / Terraform / 自定义） | 公开 |
| `/hf/*` | HuggingFace 镜像端点（`HF_ENDPOINT=http://host/hf` 兼容） | 公开 |
| `/api/cache/*` `/api/docker/*` `/api/preheat/*` 等 | 控制台 CRUD | 需登录 |
| `/api/diagnostics/*` | 诊断剧本 + bundle | 需 admin |
| `/metrics` | Prometheus metrics | 公开（生产环境 nginx 应限内网） |

所有响应统一 `application/json`；错误响应统一：

```json
{ "error": { "code": "...", "message": "..." } }
```

## 环境变量

完整列表见 `internal/config/config.go`（`Default()` 函数）。主要项：

| 变量 | 默认值 | 说明 |
|---|---|---|
| `CNCH_HTTP_ADDR` | `:8080` | HTTP 监听地址（host:port） |
| `CNCH_DATA_DIR` | `./data` | 数据目录，DB 在 `${DATA_DIR}/cncachehub.db` |
| `CNCH_CACHE_DIR` | `./cache` | blob 落盘根目录 |
| `CNCH_LOG_LEVEL` | `info` | debug / info / warn / error |
| `CNCH_UPSTREAM_REGISTRY` | `https://registry-1.docker.io` | 默认上游 Registry |
| `CNCH_UPSTREAM_TIMEOUT_SECONDS` | `60` | 上游 HTTP 超时 |
| `CNCH_MAX_OBJECT_SIZE_MB` | `1024` | 单个对象落盘上限（MB），超过走旁路 |
| `CNCH_RESERVE_SPACE_GB` | `5` | 缓存盘最小保底空间（GB） |
| `CNCH_SMALL_VPS_OPT` | `false` | 小容量 VPS 优化开关 |
| `CNCH_ADMIN_PASSWORD` | （空） | 首次 init 时设的 admin 密码；空 = 不接受 init |
| `CNCH_TRUSTED_PROXIES` | （空） | 逗号分隔 CIDR，标识哪些 RemoteAddr 来源可信任其 X-Forwarded-For（默认用 loopback+RFC1918） |
| `CNCH_SHUTDOWN_TIMEOUT_SECONDS` | `30` | Graceful shutdown 超时 |

启动时日志会打印生效配置；`admin_password` / `token` / `secret` / `api_key` / `cookie` / `authorization` 字段都会被自动脱敏为 `***`（`internal/log` redactingHandler）。

## 构建

```bash
# 本地默认平台
make build      # 产物在 ../bin/

# 交叉编译（纯 Go，无 CGO）
GOOS=linux  GOARCH=amd64 make build
GOOS=linux  GOARCH=arm64 make build
GOOS=darwin GOARCH=arm64 make build
```

打 release 制品（Linux amd64 + manifest.json + web tarball）：

```bash
make release VERSION=v0.1.2
make release-verify VERSION=v0.1.2
```

## 测试

```bash
make test    # = cd server && go test ./... -count=1
make lint    # = cd server && go vet ./...
```

CI 在 `.github/workflows/ci.yml` 跑：`go test -race + go vet + go build` / `web type-check + build` / `docker build` / `shellcheck`。

覆盖率 14 个包 66-100%（详见 AGENTS.md 表格）。

## Docker

```bash
docker build -t cncachehub:dev -f server/Dockerfile server
docker run --rm -p 8080:8080 \
  -v $(pwd)/server/data:/var/lib/cncachehub/data \
  cncachehub:dev
```

镜像用 `golang:1.23-alpine` 编译 → `alpine:3.19` 运行；非 root 用户（uid 10001）；
内置 `HEALTHCHECK` 走 `/healthz`。

## 目录结构

```
server/
├── cmd/cncachehub/main.go        # 入口
├── internal/
│   ├── access/                   # /v2 /r 访问控制 (Token + IP 白名单)
│   ├── api/                      # chi 路由 + middleware + handlers
│   ├── cache/                    # FileStore (LRU + cap)
│   ├── clientip/                 # safe client IP extraction (trusted proxy CIDR)
│   ├── config/                   # 环境变量加载与校验
│   ├── crypto/                   # AES-256-GCM master key / credential cipher
│   ├── diagnostics/              # 诊断剧本 + bundle 导出
│   ├── dns/                      # 内置 mini DNS server (SteamCMD)
│   ├── log/                      # slog JSON + 敏感字段脱敏
│   ├── metrics/                  # Prometheus metrics
│   ├── preheat/                  # 预热任务执行器 (docker / steam / resource / hf_model)
│   ├── proxy/                    # Docker / 资源 / HF 镜像反代
│   ├── ratelimit/                # token-bucket 限流
│   └── storage/                  # SQLite 封装 + migrations runner
│       └── migrations/0001..0017
├── go.mod
├── go.sum
├── Dockerfile
└── README.md
```
