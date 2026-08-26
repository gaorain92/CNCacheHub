# CNCacheHub · Web 控制台

> Vue 3 + Vite + TypeScript + Tailwind + Element Plus + Pinia
> v0.1.1 — 11 个业务页面（Dashboard / Docker / SteamCMD / Cache / Logs / Settings / Clients / Preheat / Resources / HuggingFace / Diagnostics）

完整产品需求见仓库根 `docs/prd.md`；项目规则见 `AGENTS.md`。

## 前置

- Node.js **20+**（项目用 `node:20-alpine` 构建）
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
├── api/             axios 实例 + 业务 API 客户端
│   ├── client.ts                 # 拦截器：401 跳转 / 错误处理 / 401 whitelist
│   ├── huggingface.ts            # HF tree + preheat
│   ├── client-config.ts          # daemon.json / containerd / k3s 配置生成
│   └── ...                       # 其它（preheat / access / cache / settings ...）
├── components/      公共组件
│   ├── Sidebar.vue               # 主侧边栏
│   ├── TopBar.vue
│   ├── DaemonJsonBlock.vue       # /api/docker/daemon.json 渲染
│   └── ...                       # 其它（Card / Dialog / Table wrapper）
├── layouts/         AppLayout
├── router/          11 个页面 + 404 / 401 / 403，全部懒加载
├── stores/          Pinia: useAuth / useDocker / usePreheat / useSettings / ...
├── i18n/            zh-CN + en 翻译
├── types/           API DTO 类型（camelCase）
├── views/           11 个业务页面
│   ├── DashboardView
│   ├── DockerView / CacheView / LogsView / SettingsView
│   ├── SteamCMDView / ClientsView / PreheatView / ResourcesView
│   ├── HuggingFaceView
│   └── DiagnosticsView
├── utils/           公共工具（clipboard 等）
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

- 路由级单元测试（目前靠 `vue-tsc` 严格模式保证类型）
- 通知（邮件）UI（webhook 部分后端已实现）
- 完整的 E2E 测试套件
