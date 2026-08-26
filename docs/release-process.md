# CNCacheHub 发布流程

> 从源码 commit 到用户能 `install.sh` 装上 — 全链路。

---

## 1. 总览

```
┌─────────────────────────────────────────────────────────────────────┐
│                          开发者本地                                  │
│  git tag v0.1.1 && git push --tags                                  │
└────────────────────────────────┬────────────────────────────────────┘
                                 │
                                 ▼
┌─────────────────────────────────────────────────────────────────────┐
│                      GitHub Releases API                              │
│  - release tag 创建                                                   │
│  - release asset 上传:                                               │
│      cncachehub-v0.1.1-linux-amd64.tar.gz                           │
│      cncachehub-server-v0.1.1-darwin-amd64                           │
│      cncachehub-web-v0.1.1.tar.gz                                   │
└────────────────────────────────┬────────────────────────────────────┘
                                 │
                                 ▼
┌─────────────────────────────────────────────────────────────────────┐
│                          用户机器                                     │
│  ./install.sh --source=release --version=latest                     │
│    ↓                                                                  │
│  1. 查 GitHub API 拿 latest tag (v0.1.1)                              │
│  2. 下载 cncachehub-v0.1.1-linux-amd64.tar.gz                       │
│  3. 解压 → systemd 部署 / docker 部署                                  │
└─────────────────────────────────────────────────────────────────────┘
```

`install.sh` 支持三种源码来源（`--source=`）：

| Mode | 行为 | 需要 | 适合 |
|---|---|---|---|
| `local` (默认) | 用 `$PROJECT_ROOT` 当前源码 | 本地源码 | 开发模式，迭代快 |
| `git` | git fetch + checkout ref | git | CI/CD / 多机器部署 |
| `release` | 拉 GitHub Releases 预编译 tarball | curl | 一键装最新，零依赖 |

---

## 2. 开发者：打 release

### 2.1 本地打 tag

```bash
cd cncachehub
# 1. 确认所有改动都 commit 了
git status

# 2. 跑完测试
cd server && go test ./... && cd ..
cd web && npm run type-check && npm run lint && cd ..

# 3. 打 tag（用 v 前缀）
git tag -a v0.1.1 -m "v0.1.1 — HuggingFace 镜像 + security audit + CI 修复"
git push origin main --tags
```

### 2.2 GitHub 上传 release asset

可以用 GitHub CLI：

```bash
# 安装 gh (macOS: brew install gh / Debian: apt install gh)

# 1. 编译 Linux amd64 制品
make release VERSION=v0.1.1
# （或手写：
#   cd server && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w -X main.version=v0.1.1 -X main.commit=v0.1.1" -o ../dist/cncachehub-server-v0.1.1-linux-amd64 ./cmd/cncachehub
#   cd web && npm ci && npm run build
#   tar -czf ../dist/cncachehub-web-v0.1.1.tar.gz dist/）

# 2. 合并成单一 tarball（install.sh 默认只下载这一个）
tar -czf cncachehub-v0.1.1-linux-amd64.tar.gz \
  -C dist cncachehub-server-v0.1.1-linux-amd64 \
  -C dist cncachehub-web-v0.1.1.tar.gz \
  -C dist manifest.json

# 3. 上传
gh release create v0.1.1 \
  --title "v0.1.1" \
  --notes "## Changes
- HuggingFace 镜像端点（/hf/* + /api/huggingface/*）
- nezha 风格 install-online.sh 一键安装
- security audit（clientip / body limit / 代理头过滤 / LIKE escape / path traversal / 安全头）
- CI 修复（go vet / shellcheck / docker build / 跨平台 race / linux Chtimes）
- web 复制按钮 HTTP fallback" \
  cncachehub-v0.1.1-linux-amd64.tar.gz

# 4. 验证
gh release view v0.1.1
```

### 2.3 tarball 内部结构（install.sh 期望的格式）

```bash
# 任意路径下解压后应该有：
tar -tzf cncachehub-v0.1.1-linux-amd64.tar.gz
#   ./cncachehub-server-v0.1.1-linux-amd64   # Go 静态二进制
#   ./cncachehub-web-v0.1.1.tar.gz          # web dist 打包
#   ./manifest.json                          # 版本元数据
```

`manifest.json` 模板（实际由 Makefile 生成）：

```json
{
  "name": "cncachehub",
  "version": "v0.1.1",
  "commit": "7cbc95e",
  "buildDate": "2026-08-26T05:36:19Z",
  "components": {
    "server": {
      "binary": "cncachehub-server-v0.1.1-linux-amd64"
    },
    "web": {
      "archive": "cncachehub-web-v0.1.1.tar.gz"
    }
  }
}
```

> `install.sh` 会校验 `manifest.json` 存在，否则报"不是合法 release 制品"。

### 2.4 自动化（CI）

`.github/workflows/release.yml`：

```yaml
name: Release

on:
  push:
    tags: ['v*']

jobs:
  release:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.22.10'
      - uses: actions/setup-node@v4
        with:
          node-version: '20'
      
      - name: Build server (linux-amd64)
        working-directory: server
        run: |
          CGO_ENABLED=0 GOOS=linux go build \
            -ldflags="-s -w -X main.version=${GITHUB_REF_NAME} -X main.commit=${GITHUB_SHA}" \
            -o ../dist/cncachehub-server-linux-amd64 \
            ./cmd/cncachehub
      
      - name: Build web dist
        working-directory: web
        run: |
          npm ci
          npm run build
          tar -czf ../dist/cncachehub-web.tar.gz dist/
      
      - name: Bundle release
        run: |
          cd dist
          cat > manifest.json <<EOF
          {
            "name": "cncachehub",
            "version": "${GITHUB_REF_NAME}",
            "commit": "${GITHUB_SHA}",
            "buildDate": "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
          }
          EOF
          tar -czf ../cncachehub-${GITHUB_REF_NAME}-linux-amd64.tar.gz \
            -C . cncachehub-server-linux-amd64 \
            -C . cncachehub-web.tar.gz \
            -C . manifest.json
      
      - name: Create release
        uses: softprops/action-gh-release@v2
        with:
          files: |
            cncachehub-${{ github.ref_name }}-linux-amd64.tar.gz
          generate_release_notes: true
```

---

## 3. 用户：装最新 release

### 3.1 一行装最新

```bash
curl -fsSL https://raw.githubusercontent.com/gaorain92/CNCacheHub/main/scripts/install.sh | \
  bash -s -- --source=release --runtime=systemd --admin-password=mySecret123
```

> 走 release 模式 = 拉预编译 tarball，**不需要装 Go / npm / docker**。

### 3.2 装特定版本

```bash
./install.sh --source=release --version=v0.1.0 --runtime=docker
```

### 3.3 装最新 + 自动

```bash
./install.sh --source=release --version=latest update   # 升级到 latest
```

### 3.4 装 git 最新 commit

```bash
# 测机 / 开发环境 — 用 git 拉源码 build
./install.sh --source=git --version=latest
```

要求：当前目录是 git 仓库（`$PROJECT_ROOT/.git` 存在 + 有 `origin` remote）。

或者指定 remote URL：

```bash
./install.sh --source=git --git-url=https://github.com/gaorain92/CNCacheHub.git
```

---

## 4. install.sh source 模式详解

### 4.1 local 模式（默认）

```bash
./install.sh update --mode=express
```

行为：
- 用 `$PROJECT_ROOT`（脚本所在目录的父目录）当前源码
- build go + web，部署到 systemd / docker
- **不 git pull**，不查 release
- 适合：开发者自己电脑，每次手动改完跑

### 4.2 git 模式

```bash
./install.sh init --source=git --version=v0.1.0
```

行为：
- 如果 `$PROJECT_ROOT` 是 git 仓库 + 有 origin → `git fetch --tags` + `git checkout $VERSION`
- 如果不是 → 用 `--git-url` 指定的 URL 克隆
- 然后按 local 模式 build + 部署
- 适合：CI 节点、多机部署、保证代码新鲜

`--version=latest` 在 git 模式下 = `HEAD`

### 4.3 release 模式

```bash
./install.sh init --source=release --version=latest
```

行为：
- 查 GitHub API `/repos/OWNER/REPO/releases/latest` 拿 tag
- 拉 `https://github.com/.../releases/download/{tag}/cncachehub-{tag}-linux-amd64.tar.gz`
- 解压到 `/tmp/cnch-release-{tag}-{pid}/`
- 校验 `manifest.json` 存在
- `PROJECT_ROOT` 切到 staging dir，build / install 走一样的流程
- 退出时清理 staging
- 适合：纯用户机器装 CNCacheHub，不装 Go / npm / docker

`--release-url` 自定义主机（默认 `https://github.com/gaorain92/CNCacheHub/releases`）：

```bash
./install.sh --source=release --release-url=https://gitea.example.com/cncachehub/releases
```

### 4.4 三种模式的区别

| 阶段 | local | git | release |
|---|---|---|---|
| 1. 拿源码 | 用 `$PROJECT_ROOT` | git fetch + checkout | curl 下载 tarball |
| 2. go build | 必跑 | 必跑 | 跳过（用预编译） |
| 3. npm build | 必跑 | 必跑 | 跳过（用预编译） |
| 4. 部署 | systemd/docker | systemd/docker | systemd/docker |
| 需要 go ≥ 1.22 | ✅ | ✅ | ❌ |
| 需要 node ≥ 20 | ✅ | ✅ | ❌ |
| 需要 docker | 看 runtime | 看 runtime | 看 runtime |
| 网络 | 装包时 | 装包时 + git fetch | 装包时 + GitHub |
| 装最新 | 取决于本机代码 | git pull / latest tag | 永远装最新 release |

---

## 5. 验证装好了

```bash
# 1. 服务在线
curl -sf http://localhost:8082/api/healthz | head -c 100
# 期望: {"db":"ok","status":"ok","uptime":"3s","version":"v0.1.1",...}

# 2. 版本正确
curl -s http://localhost:8082/api/version
# 期望: {"name":"cncachehub","version":"v0.1.1","commit":"v0.1.1",...}

# 3. systemd 状态
systemctl status cncachehub-server.service
# 期望: Active: active (running)

# 4. 登录
# 浏览器开 http://your-server-ip/，用户名 root，密码 = 装时设的
```

---

## 6. 升级 / 降级 / 卸载

```bash
# 升级到 latest
./install.sh update --source=release --version=latest

# 升级到特定版本
./install.sh update --source=release --version=v0.1.2

# 降级（注意数据兼容性！）
./install.sh update --source=release --version=v0.0.9

# 卸载（保留数据）
./install.sh uninstall --runtime=systemd

# 完全卸载（删数据）
./install.sh uninstall --runtime=systemd --purge
```

升级前后 binary version 用 `curl /api/version` 看对不对。

---

## 7. 自托管 release（不进 GitHub）

如果你的 CNCacheHub fork 不在 GitHub，或者 release 走自建：

```bash
# 1. 在你的 release 主机上传 tarball
# 例: 推到 https://releases.your-corp.com/cncachehub/v0.1.0/

# 2. 改用 --release-url
./install.sh \
  --source=release \
  --release-url=https://releases.your-corp.com/cncachehub \
  --version=v0.1.0
```

URL 约定（参考 GitHub releases API）：
- latest API: `https://<release-url>/latest` 返回 `{"tag_name": "vX.Y.Z"}`（或任何带 `tag_name` 字段的 JSON）
- 单个 release: `https://<release-url>/download/<version>/cncachehub-<version>-linux-amd64.tar.gz`

如果你用 Gitea：
- API: `https://gitea.example.com/api/v1/repos/owner/repo/releases/latest` 返回 `tag_name`
- 下载: `https://gitea.example.com/owner/repo/releases/download/<tag>/<asset>`

---

## 8. 故障排查

### 8.1 "源码解析失败 (source=release version=latest)"

GitHub API 调用失败。可能原因：
- rate limit（未登录 60 次/小时）
- 网络问题
- repo 是私有的，install.sh 用的默认 URL 没带 token

修法：
- 用 `--release-url` 指向镜像
- 或者用 `--source=git` 绕过 release
- 或者 `--source=local` 完全离线

### 8.2 "校验 manifest.json 失败"

tarball 不是 install.sh 期望的格式。检查：
- `manifest.json` 在 tarball 根目录
- 字段名是 `tag_name`（latest API）或 `version`（manifest）

### 8.3 binary 还是旧版

`install.sh update` 默认走 source=local — 用本机源码 build。装新版的正确姿势：

```bash
./install.sh update --source=release --version=latest
```

或者：
```bash
cd $PROJECT_ROOT && git pull && ./install.sh update --source=local
```
