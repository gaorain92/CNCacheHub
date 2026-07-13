# CNCacheHub · Web 控制台

> Vue 3 + Vite + TypeScript + Tailwind + Element Plus + Pinia
> Phase 0 骨架（无业务实现），后续阶段接入真实数据。

## 前置

- Node.js **20+** （项目用 `node:20-alpine` 构建）
- npm 10+（也可换 pnpm / yarn，自行调整 lockfile）
- 后端（`server/`）跑在 `http://localhost:8080` 才能让 Dashboard 拉到真实数据

## 开发模式

```bash
cd web
npm install
npm run dev
# → http://localhost:5173
```

Vite dev server 会把 `/api/*` 与 `/healthz` 代理到 `http://localhost:8080`，所以正常情况你应该先：

```bash
# 终端 A
cd server && go run ./cmd/cncachehub

# 终端 B
cd web && npm run dev
```

`/api/healthz` 与 `/api/version` 这两个端点后端需要按 `src/types/api.ts` 里的字段名返回（camelCase）。

## 类型检查

```bash
npm run type-check
```

零错误退出码 = 0。

## 生产构建

```bash
npm run build
# 产物在 dist/
```

构建会先跑 `vue-tsc` 类型检查，再 `vite build`。

## 本地预览构建产物

```bash
npm run preview
```

## Docker 构建

```bash
docker build -t cncachehub-web:dev .
docker run --rm -p 8081:80 cncachehub-web:dev
# → http://localhost:8081
```

Dockerfile 是多阶段：`node:20-alpine` 编译 → `nginx:alpine` 托管 `dist/`，自带 SPA fallback。

## 目录结构

```
src/
├── api/             axios 实例 + /api/healthz / /api/version
├── components/      StatusDot / Sidebar / TopBar
├── layouts/         AppLayout (侧边栏 + 顶栏 + 内容)
├── router/          8 个业务页面 + 404，全部懒加载
├── stores/          Pinia: useHealthStore
├── types/           HealthResponse / VersionResponse
├── views/           Dashboard（真实数据）+ 7 个占位 + 404
├── App.vue
├── main.ts
├── style.css
└── env.d.ts
```

## 设计风格

- 极客暗黑：`bg-ink #020617` + `panel/panel2` 渐变
- Mint（`#2dd4bf`）→ Violet（`#8b5cf6`）渐变作为强调色
- 玻璃感卡片（`backdrop-filter: blur(24px)`）、双圆点光斑背景
- 视觉参考 `prototype/index.html`，但不要求像素一致

## 已知未完成

- 业务页面（Docker / SteamCMD / Cache 等）都是占位卡片
- 未做 i18n、未做路由鉴权、未做单元测试
- 后端 `/api/healthz` / `/api/version` 字段名以后端实际返回为准
