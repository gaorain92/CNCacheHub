# CNCacheHub 产品需求文档（PRD）

> 项目定位：面向国内开发者、运维团队、游戏服务器服主的自托管下载加速中枢。以 Docker/OCI 镜像拉取加速为核心，扩展支持 SteamCMD、Steam 内容缓存以及常见开发资源下载慢的问题。

---

## 1. 文档信息

| 项目 | 内容 |
|---|---|
| 产品名称 | CNCacheHub（暂定） |
| 产品类型 | 自托管代理 / 缓存 / 下载加速网关 |
| 目标平台 | Linux 服务器、VPS、NAS、内网网关、Kubernetes 边缘节点 |
| 首发形态 | Docker Compose 一键部署 + Web 控制台 + 客户端配置生成器 |
| 核心对象 | Docker/OCI 镜像、SteamCMD/Steam 内容、常见开发资源下载源 |
| PRD 版本 | v1.0 |
| 语言 | 简体中文 |

---

## 2. 产品概述

CNCacheHub 是一个面向国内网络环境的自托管加速网关，帮助用户把 Docker 镜像、SteamCMD 游戏服务端文件、GitHub Release、AI 模型、测试浏览器二进制、云原生工具包等高频慢资源，通过统一入口进行代理、缓存、预热和诊断。

产品不做侵入式破解，不做未授权内容分发，不做 TLS 中间人解密。它的核心价值是：

- 让用户用很低的部署成本搭建自己的 Docker 镜像加速入口。
- 支持 Docker Hub、GHCR、Quay、registry.k8s.io 等 OCI Registry 的代理缓存。
- 通过 DNS + HTTP 缓存模式支持 SteamCMD / Steam 内容下载加速，适合游戏服主、工作室、内网多机器重复部署场景。
- 扩展覆盖 GitHub Release、Hugging Face、Playwright/Puppeteer、Terraform/Helm 等国内常见慢资源。
- 针对 10-40GB 小容量 VPS 提供省盘模式，默认限制大文件落盘，避免缓存服务吃满系统盘。
- 提供可复制的客户端配置，减少用户查教程、改配置、排错的时间。
- 提供缓存命中率、上游可用性、下载速度、错误原因等可观测信息。

一句话：**把「Docker 拉不动、SteamCMD 下载慢、GitHub/HF/测试浏览器资源卡住、配置又麻烦」变成「部署一次，生成配置，复制就能用」。**

### 2.1 需求执行口径

后续所有设计、开发、原型、文档和 AI 辅助生成都必须遵守以下口径：

- **第一优先级：可用性**。Docker Hub 公共镜像加速必须先稳定可用，不能被扩展模块拖慢主线。
- **第二优先级：可控性**。默认不开通用开放代理，不缓存敏感 URL，不绕过平台授权。
- **第三优先级：省心接入**。每个模块必须提供配置生成、验证命令、失败诊断、回滚方案。
- **第四优先级：小容量 VPS 安全**。任何缓存写入都必须考虑磁盘上限、对象大小、保底空间和旁路策略。
- **第五优先级：可观测**。每一次代理请求都要能解释：来自谁、请求什么、命中与否、失败原因、下一步怎么修。

---

## 3. 背景与痛点

### 3.1 Docker/OCI 镜像下载痛点

国内用户经常遇到：

- Docker Hub 连接不稳定、速度慢、超时。
- Docker Hub 匿名拉取受限，CI/CD 经常被 rate limit 卡住。
- Kubernetes 相关镜像分散在 registry.k8s.io、gcr.io、quay.io、ghcr.io 等源，配置复杂。
- 团队内多台机器重复拉取相同镜像，浪费带宽和时间。
- 公共镜像站可用性不可控，变更频繁，安全和隐私不可控。

### 3.2 SteamCMD / 游戏服务端下载痛点

国内游戏服主、运维、工作室经常遇到：

- SteamCMD 下载服务端文件速度慢、连接抖动、反复失败。
- 多台机器部署同一个游戏服务端时，每台都重复下载几十 GB 内容。
- SteamCMD 报错信息不直观，用户难判断是 DNS、CDN、网络、磁盘还是账号权限问题。
- 现有 LANCache 方案偏工程化，配置 DNS、域名列表、缓存目录、证书、端口映射对普通用户不友好。

### 3.3 现有方案不足

| 方案 | 问题 |
|---|---|
| 公共 Docker 镜像站 | 可用性不稳定，域名可能失效，安全不可控 |
| 手写 registry mirror | 只解决 Docker Hub，无法统一管理多 registry |
| HTTP 代理 | Docker/SteamCMD 配置不统一，缓存能力弱 |
| LANCache | 功能强但上手门槛高，缺少中文可视化和诊断 |
| 手动改 hosts/DNS | 容易误配，排错困难，不适合团队批量使用 |

### 3.4 其他高频慢资源痛点

除了 Docker 和 SteamCMD，国内开发/运维/AI/测试场景还经常被以下资源卡住：

| 资源类型 | 常见失败点 | 影响 |
|---|---|---|
| GitHub Release / Raw / LFS | 安装脚本、二进制包、源码压缩包下载失败 | 部署脚本卡住，CI/CD 不稳定 |
| Hugging Face / AI 模型 | safetensors、gguf、dataset、tokenizer 拉取慢 | AI 服务部署失败，大文件重复下载 |
| Playwright / Puppeteer / Cypress | 浏览器二进制下载失败 | 自动化测试环境无法初始化 |
| Terraform / Helm / 云原生工具 | provider、chart、kubectl/helm/k9s 二进制慢 | K8s/IaC 初始化失败 |
| 语言包管理器 | npm/pip/go/maven/rustup 的大包或二进制依赖慢 | 构建时间长，缓存不可控 |
| Minecraft / Mod 服务端生态 | Paper、Fabric、Forge、Modrinth/CurseForge 资源慢 | 游戏服搭建体验差 |

这些资源不应一开始全部做成全功能代理，而应作为「白名单资源缓存」逐步扩展，核心仍然是自托管、可控、可诊断。

---

## 4. 产品目标

### 4.1 核心目标

1. **一键部署**：用户在一台 VPS/NAS/服务器上通过 Docker Compose 快速启动服务。
2. **Docker 加速优先**：首发必须稳定支持 Docker Hub 公共镜像代理缓存。
3. **多源扩展**：支持 GHCR、Quay、registry.k8s.io、gcr.io 等常见 OCI Registry。
4. **SteamCMD 加速**：提供 Steam 内容缓存与 SteamCMD 使用向导，解决重复下载与慢速问题。
5. **资源加速中心**：通过白名单方式扩展 GitHub、Hugging Face、Playwright、Terraform、Helm 等常见慢资源。
6. **小容量 VPS 友好**：同一个项目内提供可选优化开关，开启后避免大文件缓存导致系统盘爆满。
7. **配置自动生成**：自动生成 Docker、containerd、nerdctl、Kubernetes、SteamCMD、浏览器二进制镜像等配置。
8. **可观测可排错**：提供连接测试、上游健康、缓存命中、下载速度、错误定位。
9. **安全自托管**：默认只代理公开资源，敏感凭据可选配置且加密存储。

### 4.2 非目标

- 不提供绕过版权、地区限制或平台授权的能力。
- 不破解 Steam/SteamCMD 登录、Depot 权限或付费内容访问限制。
- 不做 HTTPS 明文解密或 MITM 缓存。
- 不承诺替代所有公共镜像站，只提供可自控、可缓存的私有加速入口。
- 不在首版支持复杂企业级多租户计费。

---

## 5. 目标用户

### 5.1 个人开发者

- 使用 Docker 进行本地开发、部署小项目。
- 痛点：拉基础镜像慢、经常换镜像源、教程失效。
- 诉求：有一个自己可控的 Docker 加速地址，复制配置即可用。

### 5.2 独立运维 / 小团队 DevOps

- 管理多台服务器、CI runner、测试环境。
- 痛点：构建时频繁拉镜像，耗时且不稳定。
- 诉求：团队内统一缓存，提升 CI 成功率和构建速度。

### 5.3 Kubernetes 用户

- 使用 containerd、k3s、k8s、rke2、边缘节点。
- 痛点：镜像源分散，pause/coredns/ingress 镜像拉取失败。
- 诉求：生成 containerd hosts.toml、registries.yaml 等配置。

### 5.4 游戏服务器服主 / 工作室

- 使用 SteamCMD 部署 CS2、Palworld、Valheim、ARK、Rust 等服务端。
- 痛点：SteamCMD 下载慢，多台机器重复下载。
- 诉求：通过缓存网关和 DNS 配置，让同一 AppID 后续下载明显加速。

### 5.5 NAS / Homelab 用户

- 在家用 NAS 或家庭服务器上部署服务。
- 痛点：网络环境复杂，希望图形化管理。
- 诉求：内网统一缓存 Docker 镜像和常用下载资源。

---

## 6. 核心场景

### 场景 A：个人 VPS 上快速搭建 Docker 加速

用户在一台网络较好的 VPS 上运行安装命令，Web 控制台显示 Docker Hub 加速地址。用户复制 `daemon.json` 配置到本地或目标服务器，重启 Docker 后即可通过该地址拉取镜像。

### 场景 B：团队 CI/CD 复用镜像缓存

团队管理员部署 CNCacheHub，配置 CI runner 的 Docker daemon 或 buildkit mirror。第一次构建拉取镜像走上游，后续构建命中本地缓存，构建耗时下降。

### 场景 C：Kubernetes 节点批量配置多 Registry Mirror

用户选择运行环境为 containerd/k3s/rke2，系统生成对应配置和验证命令。管理员批量下发到节点，节点拉取 `registry.k8s.io/pause`、`quay.io` 等镜像时通过 CNCacheHub。

### 场景 D：SteamCMD 游戏服多机器加速

服主在内网或同机部署 CNCacheHub 的 Steam 缓存模块。系统生成 DNS 配置和 SteamCMD 启动示例。第一台机器下载游戏服务端后，后续机器下载相同内容命中缓存，速度显著提升。

### 场景 E：下载失败诊断

用户在控制台输入镜像名或 Steam AppID，系统检查 DNS、端口、上游连接、缓存目录、磁盘空间、TLS 证书、请求日志，输出明确的失败原因和修复建议。

---

## 7. 成功指标

### 7.1 产品指标

| 指标 | 目标 |
|---|---|
| 首次部署成功率 | >= 90% |
| 首次 Docker 配置完成时间 | <= 10 分钟 |
| Docker Hub 公共镜像首次拉取成功率 | >= 95% |
| 已缓存镜像二次拉取速度提升 | >= 3 倍，内网场景 >= 10 倍 |
| SteamCMD 同一 AppID 二次下载缓存命中率 | >= 70% |
| 常见错误可诊断覆盖率 | >= 80% |
| Web 控制台关键操作响应 | <= 1 秒 |

### 7.2 用户体验指标

- 新手不需要理解 registry mirror 原理，也能完成 Docker 加速配置。
- SteamCMD 加速至少提供「DNS 模式」「Docker 运行模式」两条明确路径。
- 所有生成配置都提供复制按钮、适用系统说明、验证命令和回滚方式。

---

## 8. 产品范围与优先级

### 8.1 P0：首发必须完成

| 模块 | 功能 | 说明 |
|---|---|---|
| 安装部署 | Docker Compose 一键部署 | 包含核心网关、Web UI、缓存目录、日志目录 |
| 初始化向导 | 域名/IP、端口、存储路径配置 | 支持无域名 IP 模式；支持后续绑定域名/TLS |
| Docker Hub 代理缓存 | Docker Hub pull-through cache | 支持公开镜像；支持 `library/*` 兼容 |
| Docker 配置生成器 | daemon.json 生成 | 支持 Linux Docker Engine；包含验证命令 |
| 缓存管理 | 缓存容量、目录、清理策略 | 可查看总占用、按模块清理 |
| 小容量 VPS 优化 | 低磁盘/低内存优化开关 | 在同一项目内可选开启，开启后限制缓存上限和大文件写入 |
| 定时清理任务 | 自动清理计划与保留规则 | 支持按时间/容量/最近访问清理，执行前预估释放空间 |
| 请求日志 | 镜像拉取日志 | 显示状态码、耗时、缓存命中、客户端 IP |
| 健康检查 | 上游连通性检查 | 检查 Docker Hub token、registry API、DNS、磁盘 |
| Web 控制台 | 首页仪表盘 | 显示服务状态、命中率、流量、错误数 |
| 安全基础 | 控制台登录 | 管理员账号、密码初始化、会话管理 |
| 文档导出 | 快速接入说明 | 控制台内置中文教程 |

### 8.2 P1：增强版本

| 模块 | 功能 | 说明 |
|---|---|---|
| 多 Registry 支持 | GHCR、Quay、registry.k8s.io、gcr.io | 每个 Registry 独立上游、独立缓存策略 |
| containerd 配置生成 | hosts.toml / certs.d | 支持 containerd、nerdctl、k3s、rke2 |
| SteamCMD 加速 | Steam 内容缓存模块 | DNS + HTTP 缓存，兼容 LANCache 域名策略 |
| SteamCMD 向导 | AppID 配置、启动示例、DNS 配置 | 支持 Linux、Docker、内网 DNS 指南 |
| 预热任务 | 镜像预拉取、Steam AppID 预热 | 任务队列、失败重试、进度展示 |
| 资源加速中心首批 | GitHub Release、Playwright/Puppeteer、Terraform/Helm、Hugging Face | 白名单资源缓存、配置生成、空间预估 |
| 权限控制 | 镜像代理访问 Token | 避免公网开放后被滥用 |
| TLS 管理 | 自动证书 / 手动证书 | 支持 Let's Encrypt、反代模式、内网自签 |
| 通知 | 下载失败/磁盘告警 | 支持 Webhook/飞书/Telegram（后续可插拔） |

### 8.3 P2：高级能力

| 模块 | 功能 | 说明 |
|---|---|---|
| 通用下载加速 | npm、pip、Go、Maven、Rust、Homebrew 等包管理资源 | 可配置白名单源，不做全量代理 |
| 多节点 | 边缘缓存节点 | 主控 + 多缓存节点，适合团队/机房 |
| SSO | OIDC / LDAP 登录 | 企业场景 |
| 配额与审计 | 用户级流量、缓存、审计报表 | 多团队共享场景 |
| API 自动化 | 完整 OpenAPI | 便于 CI/CD 接入 |
| 高级清理策略 | LRU、按镜像保留、按 AppID 保留 | 降低磁盘压力 |
| 镜像安全扫描 | 接入 Trivy 等工具 | 可选，不阻塞下载加速主线 |

---

## 9. 核心功能需求

## 9.1 安装与初始化

### 9.1.1 一键部署

用户通过一条命令生成部署目录并启动服务：

```bash
curl -fsSL https://example.com/install.sh | bash
```

安装完成后输出：

- Web 控制台地址。
- 初始管理员账号生成方式。
- Docker Hub mirror 地址。
- 常用配置入口。
- 日志和缓存目录位置。

### 9.1.2 初始化向导

首次打开控制台时进入初始化向导：

1. 设置管理员账号密码。
2. 选择部署模式：
   - 单机 IP 模式。
   - 域名 + TLS 模式。
   - 内网缓存网关模式。
3. 配置缓存根目录与容量上限。
4. 选择缓存策略：
   - 标准缓存策略：适合磁盘空间充足的 VPS/NAS/服务器，缓存命中优先。
   - 小容量 VPS 优化：可选开关，开启后优先保留系统空间，限制超大对象落盘。
   - 自定义策略：用户手动配置缓存上限、保底空间、单对象大小和清理水位。
5. 选择启用模块：
   - Docker Hub 加速。
   - 多 Registry 加速。
   - SteamCMD 加速。
   - 通用下载加速。
6. 运行环境自检。
7. 生成首份客户端配置。

### 9.1.3 部署环境要求

| 项目 | 最低要求 | 推荐 |
|---|---|---|
| CPU | 1 核 | 2 核+ |
| 内存 | 512 MB | 2 GB+ |
| 磁盘 | 10 GB（建议开启小容量 VPS 优化，仅推荐 Docker 轻量缓存） | 100 GB+ SSD |
| 系统 | Debian 12 / Ubuntu 22.04 / CentOS Stream | Debian 12 / Ubuntu 24.04 |
| 网络 | 可访问目标上游 | 稳定国际出口或优质线路 |
| 端口 | 80/443/5000/8080 可配置 | 443 统一入口 |

### 9.1.4 小容量 VPS 优化设置

小容量 VPS 是重要目标场景，但不应拆成独立产品或独立原型。CNCacheHub 始终是同一个项目，只是在初始化向导和系统设置中提供「小容量 VPS 优化」开关。

开启条件：

- 用户在初始化向导中手动开启。
- 或系统检测到可用磁盘较小（例如 < 40GB）时推荐开启，但不强制。
- 用户可随时在系统设置中关闭、调整或切换为自定义策略。

开启后的默认行为：

- Docker/OCI 模块默认启用，SteamCMD、AI 模型、通用大文件缓存默认限制落盘或旁路。
- 默认缓存上限不超过磁盘可用空间的 50%，并预留至少 5GB 系统保底空间。
- 默认最大单对象缓存大小为 1GB；用户可在高级设置中改为 256MB、512MB、2GB 或自定义。
- 默认只缓存命中价值高的对象：manifest、常用基础镜像层、最近访问的 blob。
- 默认启用容量守护：超过 70% 进入告警，超过 80% 清理到 60%，低于 2GB 可用空间时进入只读旁路状态。
- 预热任务必须显示预计占用，超过配额时阻止创建或建议外接存储。
- SteamCMD 在开启优化时提示「不建议本地缓存大型游戏服务端」，可选择外接磁盘/S3/R2/NAS 作为缓存后端。
- 日志保留缩短：详细日志默认 3 天，聚合指标默认 14 天。
- 禁用高成本后台任务或降低频率，例如低价值全量索引、频繁健康扫描。

设置项：

| 设置 | 默认值 | 说明 |
|---|---:|---|
| 启用小容量 VPS 优化 | 关闭，低磁盘时推荐开启 | 统一项目内的策略开关 |
| 缓存总上限 | 可用磁盘 50%，最高 20GB | 开启后生效 |
| 系统保底空间 | 5GB | 低于阈值进入只读旁路 |
| 最大单对象落盘 | 1GB | 超过则只转发不落盘 |
| Docker 配额优先级 | 最高 | 避免 Steam/AI 大文件挤占 Docker 缓存 |
| 清理触发水位 | 80% | 超过后自动清理 |
| 清理目标水位 | 60% | 清理到目标水位后停止 |
| 大文件处理 | 旁路 | 可改为阻止或缓存 |

---

## 9.2 Docker/OCI 镜像代理缓存

### 9.2.1 Docker Hub 加速

系统提供一个 Docker Hub mirror endpoint，例如：

```text
https://docker.example.com
```

用户在 Docker daemon 中配置：

```json
{
  "registry-mirrors": ["https://docker.example.com"]
}
```

功能要求：

- 支持 Docker Hub 公共镜像拉取。
- 支持 official image 简写，如 `nginx:latest`、`redis:7`。
- 支持 namespace 镜像，如 `bitnami/postgresql:16`。
- 支持 manifest、blob、tag list 等 Registry API 请求。
- 支持缓存层文件，二次请求直接返回本地缓存。
- 支持上游 token 获取与匿名访问。
- 支持 Docker Hub rate limit 错误识别与提示。

### 9.2.2 多 Registry 代理

每个上游 Registry 独立配置代理入口：

| 上游 | 推荐入口示例 | 说明 |
|---|---|---|
| Docker Hub | `https://docker.example.com` | Docker daemon mirror 原生支持 |
| GHCR | `https://ghcr.example.com` | containerd/hosts.toml 优先 |
| Quay | `https://quay.example.com` | Kubernetes 常用 |
| registry.k8s.io | `https://k8s.example.com` | Kubernetes 系统镜像 |
| gcr.io | `https://gcr.example.com` | 旧版 GCR 镜像 |

功能要求：

- 每个 Registry 可独立启停。
- 每个 Registry 可配置上游地址、缓存大小、保留策略。
- 支持健康检查。
- 支持请求统计和错误统计。
- 支持只读代理，不支持首发 push。

### 9.2.3 镜像预热

用户可创建预热任务：

```text
nginx:latest
redis:7
postgres:16
registry.k8s.io/pause:3.9
quay.io/prometheus/prometheus:v2.52.0
```

预热功能要求：

- 支持批量输入镜像名。
- 自动识别 Registry。
- 显示 manifest 获取、layer 下载、缓存写入进度。
- 支持失败重试。
- 任务完成后显示缓存大小和耗时。
- 支持定时预热常用镜像。

### 9.2.4 缓存一致性

- Manifest 默认短缓存，Blob 默认长期缓存。
- tag manifest 需要按 TTL 定期重新校验。
- digest 拉取的内容可视为不可变资源。
- 用户可手动刷新某个 tag。
- 用户可锁定某些镜像长期保留。

---

## 9.3 SteamCMD / Steam 内容加速

### 9.3.1 支持模式

SteamCMD 加速提供两种主模式：

#### 模式 A：DNS + 内容缓存模式（推荐）

适合内网多机器、游戏服集群、工作室环境。

工作方式：

1. CNCacheHub 启动 DNS 服务。
2. DNS 将 Steam 内容域名解析到 CNCacheHub 缓存网关。
3. SteamCMD 请求内容 CDN 时经过缓存网关。
4. 首次下载写入缓存，后续相同内容命中缓存。

适用对象：

- 同一内网多台游戏服务器。
- 同一台宿主机多个 SteamCMD 容器。
- NAS/Homelab 环境。

#### 模式 B：SteamCMD Docker 包装模式

适合新手和单机使用。

系统提供示例命令：

```bash
docker run --rm \
  --dns <CNCacheHub_IP> \
  -v /data/steamapps:/steamapps \
  cm2network/steamcmd \
  +login anonymous +app_update <APP_ID> validate +quit
```

系统自动解释：

- DNS 为什么要指向 CNCacheHub。
- 下载目录如何挂载。
- 哪些 AppID 支持 anonymous。
- 登录态和付费内容如何由用户本地 SteamCMD 自行处理。

### 9.3.2 Steam 域名策略

系统内置 Steam/LANCache 常见域名列表，支持版本更新：

```text
*.steamcontent.com
*.steampipe.steamcontent.com
*.steamserver.net
*.steamstatic.com
content*.steampowered.com
client-download.steampowered.com
```

要求：

- 域名列表可在线更新或手动导入。
- 支持启用/禁用单个域名规则。
- 支持 DNS 测试，显示客户端实际解析结果。
- 支持与用户现有 DNS 服务器共存：上游转发、仅特定域名劫持。

### 9.3.3 SteamCMD AppID 管理

用户可在控制台维护常用 AppID：

| 字段 | 说明 |
|---|---|
| AppID | Steam 应用 ID |
| 名称 | 游戏/服务端名称 |
| 登录方式 | anonymous / 需要账号 |
| 安装目录 | 示例目录 |
| 最近预热时间 | 任务记录 |
| 缓存占用 | 内容缓存估算 |
| 命中率 | 二次下载命中情况 |

首批内置常见服务端模板：

- CS2 Dedicated Server
- Palworld Dedicated Server
- Valheim Dedicated Server
- Rust Dedicated Server
- ARK: Survival Ascended Dedicated Server
- Project Zomboid Dedicated Server
- Terraria Dedicated Server

### 9.3.4 SteamCMD 诊断

用户输入 AppID 或上传/粘贴 SteamCMD 日志后，系统分析：

- 是否能访问 Steam 登录/内容服务。
- DNS 是否命中 CNCacheHub。
- 缓存网关是否收到请求。
- 上游响应是否异常。
- 是否 anonymous 权限不足。
- 磁盘空间是否不足。
- 是否存在文件校验失败。
- 是否请求了无法缓存的加密/私有内容。

输出格式：

```text
诊断结论：DNS 未生效
原因：客户端仍将 steamcontent.com 解析到公网 CDN，而不是 CNCacheHub。
修复：将 SteamCMD 容器添加 --dns 192.168.1.10，或在宿主机网络设置中把 DNS 指向 192.168.1.10。
验证：运行 nslookup client-download.steampowered.com 192.168.1.10。
```

### 9.3.5 Steam 加速边界

必须在产品内明确说明：

- 只加速用户有权下载的内容。
- 不绕过 Steam 账号、订阅、付费或地区权限。
- 私有/加密内容是否可缓存取决于 SteamCMD 实际请求和授权结果。
- HTTPS 无法在不做 MITM 的情况下缓存明文内容；系统仅缓存可被合法代理缓存的内容。

---

## 9.4 资源加速中心

资源加速中心用于扩展 Docker/Steam 之外的国内常见慢资源。该模块必须坚持白名单、可诊断、可旁路原则，不能演变成无限制开放代理。

### 9.4.1 资源优先级矩阵

| 优先级 | 类型 | 示例 | 核心价值 | 默认策略 |
|---|---|---|---|---|
| P1 | GitHub Release / Raw / LFS | release assets、raw.githubusercontent.com、codeload、objects | 安装脚本和 CI/CD 成功率提升 | 白名单缓存 + 资源预热 |
| P1 | 测试浏览器二进制 | Playwright、Puppeteer、Cypress、ChromeDriver、GeckoDriver | 自动化测试和 CI 初始化稳定 | 镜像地址生成 + 二进制缓存 |
| P1 | 云原生工具与 IaC | Terraform Provider、Helm Chart、kubectl/helm/k9s/kind | K8s/IaC 初始化稳定 | Provider/Chart/Release 缓存 |
| P1 | AI 模型与数据 | Hugging Face safetensors、gguf、datasets、tokenizer | AI 服务部署和模型复用 | 大文件缓存 + 可选小容量优化旁路 |
| P2 | 语言包管理器 | npm/pnpm、pip/Poetry、Go、Maven、Gradle、Rust crates | 构建加速 | 包文件缓存，优先公开资源 |
| P2 | 系统与开发工具 | Homebrew bottles、rustup、Node.js dist、Python installers | 开发环境初始化 | 白名单域名缓存 |
| P2 | 游戏 Mod / 服务端生态 | Modrinth、CurseForge、PaperMC、Fabric、Forge | 游戏服搭建体验 | 合法 API 下载缓存 |
| 谨慎 | 任意 URL / 平台客户端 | Epic、Battle.net、任意下载链接 | 风险高、边界不清 | 默认不支持或仅私有白名单 |

### 9.4.2 GitHub 资源加速

支持范围：

- GitHub Release 附件。
- `raw.githubusercontent.com` 文本资源。
- `codeload.github.com` 源码压缩包。
- `objects.githubusercontent.com` 大文件对象。
- Git LFS 公开文件。

要求：

- 用户可按 repo 配置白名单，例如 `owner/repo`。
- 支持订阅最新 Release 并自动预热指定文件模式，例如 `linux-amd64.tar.gz`。
- 支持安装脚本场景的 raw 文件缓存，但必须有 TTL，避免长期返回过期脚本。
- 对带 token、签名、临时 query 的 URL 默认不缓存，只转发并脱敏记录。
- 请求日志显示 repo、release/tag、asset 名称、缓存状态和上游错误。

### 9.4.3 AI 模型资源加速

支持范围：

- Hugging Face 模型文件：`.safetensors`、`.gguf`、`.bin`、`.onnx`。
- tokenizer/config 等小文件。
- datasets 公开文件。
- PyTorch/CUDA 大 wheel 可作为 P2 扩展。

要求：

- 支持按模型名配置白名单，例如 `Qwen/Qwen2.5-7B-Instruct`。
- 支持大文件断点续传和完整性校验。
- 支持模型预热任务，创建前必须预估磁盘占用。
- 小容量 VPS 优化下，超过单对象上限的模型文件默认旁路，不落盘。
- 不保存用户 Hugging Face Token，除非后续提供本地加密凭据保险箱且默认关闭。

### 9.4.4 测试浏览器二进制加速

支持范围：

- Playwright browsers。
- Puppeteer Chromium。
- Cypress binary。
- ChromeDriver / GeckoDriver / WebDriver 相关 release。

配置生成器需要输出对应环境变量：

```bash
export PLAYWRIGHT_DOWNLOAD_HOST="https://cache.example.com/playwright"
export PUPPETEER_DOWNLOAD_BASE_URL="https://cache.example.com/puppeteer"
export CYPRESS_DOWNLOAD_MIRROR="https://cache.example.com/cypress"
```

要求：

- 识别客户端平台：linux-x64、linux-arm64、darwin-arm64、windows-x64。
- 对浏览器版本和平台建立索引。
- CI 场景提供一键复制配置和验证命令。
- 小容量 VPS 优化下优先缓存最近使用版本，旧版本自动清理。

### 9.4.5 Terraform / Helm / 云原生工具加速

支持范围：

- Terraform provider zip。
- Terraform module source archive。
- Helm chart repo index 和 chart 包。
- kubectl、helm、k9s、kind、k3s、rke2、nerdctl、containerd、runc 等 release 二进制。

要求：

- 支持 Terraform provider mirror 配置生成。
- 支持 Helm repo proxy/cache 配置生成。
- 支持常见云原生工具版本预热。
- 诊断中心能识别 provider/checksum 错误、chart index 过期、release asset 上游超时。

### 9.4.6 语言包管理器加速

P2 可选扩展：

| 类型 | 示例 | 策略 |
|---|---|---|
| npm / pnpm / yarn | npm registry tarball、GitHub tarball 依赖 | tarball 缓存，不替代完整私有 npm |
| pip / Poetry | PyPI package files、PyTorch wheel | 包文件缓存，大 wheel 支持预估空间 |
| Go | `proxy.golang.org`、`sum.golang.org`、GitHub fallback | Go module proxy/cache |
| Maven / Gradle | Maven Central、Gradle distribution | artifact 和 wrapper distribution 缓存 |
| Rust | crates.io、rustup dist | crate tarball 和 toolchain 缓存 |
| Homebrew | bottles、portable-ruby | 白名单缓存，不做完整 Homebrew 镜像 |

### 9.4.7 通用下载边界

- 通用下载模块默认关闭，避免变成不可控开放代理。
- 启用时必须配置白名单域名、路径规则和访问控制。
- 默认不缓存带认证、签名、token、session、临时 query 的 URL。
- 默认不支持任意用户提交 URL 后直接公网代理。
- 对版权、账号授权或平台协议风险高的资源，只允许用户自建私有白名单，不做公共模板。
- 所有通用资源请求都必须支持诊断：DNS、上游状态、缓存策略、旁路原因、磁盘风险。

---

## 9.5 客户端配置生成器

### 9.5.1 Docker Engine 配置

输入：

- 客户端系统：Debian/Ubuntu/CentOS/macOS Docker Desktop。
- 加速域名/IP。
- 是否启用 TLS。

输出：

- `daemon.json`。
- 写入命令。
- 重启 Docker 命令。
- 验证命令。
- 回滚命令。

示例输出：

```bash
sudo mkdir -p /etc/docker
sudo tee /etc/docker/daemon.json <<'EOF'
{
  "registry-mirrors": ["https://docker.example.com"]
}
EOF
sudo systemctl restart docker
docker pull nginx:latest
```

### 9.5.2 containerd / nerdctl 配置

支持生成：

- `/etc/containerd/certs.d/docker.io/hosts.toml`
- `/etc/containerd/certs.d/ghcr.io/hosts.toml`
- `/etc/rancher/k3s/registries.yaml`
- nerdctl mirror 配置

要求：

- 明确告诉用户是否需要 `systemctl restart containerd`。
- 明确告诉用户 k3s/rke2 的配置路径不同。
- 提供测试命令：`crictl pull` / `nerdctl pull`。

### 9.5.3 SteamCMD 配置

输出：

- Linux DNS 配置步骤。
- Docker `--dns` 示例。
- Docker Compose 示例。
- 常见 AppID 命令模板。
- 验证 DNS 命中命令。
- 失败日志检查方法。

### 9.5.4 配置包导出

用户可一键导出 zip：

```text
cncachehub-client-config.zip
├── docker/daemon.json
├── containerd/docker.io/hosts.toml
├── k3s/registries.yaml
├── steamcmd/docker-compose.yml
├── resource-accelerators/
│   ├── playwright.env
│   ├── puppeteer.env
│   ├── terraformrc
│   └── helm-repos.sh
├── verify.sh
└── README.md
```

### 9.5.5 资源加速配置

配置生成器还必须支持资源加速中心的接入配置：

| 场景 | 输出内容 |
|---|---|
| GitHub Release / Raw | curl/wget 代理地址、白名单规则、验证命令 |
| Playwright | `PLAYWRIGHT_DOWNLOAD_HOST` 与 `npx playwright install` 验证命令 |
| Puppeteer | `PUPPETEER_DOWNLOAD_BASE_URL` 与安装验证命令 |
| Cypress | `CYPRESS_DOWNLOAD_MIRROR` 与 binary 验证命令 |
| Terraform | provider mirror 配置、`.terraformrc` 示例、checksum 注意事项 |
| Helm | Helm repo proxy 地址、`helm repo add/update` 验证命令 |
| Hugging Face | 模型白名单、预热任务、断点续传提示、小容量 VPS 优化旁路提示 |

每个输出都必须包含：配置内容、复制按钮、验证命令、失败诊断入口、回滚方式。

---

## 9.6 缓存管理

### 9.6.1 缓存总览

显示：

- 总缓存占用。
- Docker/OCI 缓存占用。
- Steam 缓存占用。
- 通用下载缓存占用。
- 最近 24 小时新增缓存。
- 磁盘剩余空间。
- 命中率趋势。

### 9.6.2 清理策略

支持：

- 按模块清理。
- 按 Registry 清理。
- 按镜像/namespace 清理。
- 按 Steam AppID 清理。
- 按最近访问时间清理。
- 超过容量阈值自动 LRU 清理。
- 白名单保留：重要镜像/AppID 永不自动清理。

### 9.6.3 定时清理任务

系统必须支持可视化配置定时清理任务，避免缓存长期增长导致磁盘耗尽。

任务触发方式：

- 按固定周期执行：每天、每周、每月、自定义 Cron 表达式。
- 按容量阈值执行：缓存总占用超过 70% / 80% / 90% 时触发。
- 按模块阈值执行：Docker、Steam、通用下载分别设置容量上限。
- 按低峰时间窗口执行：例如每天 03:00-05:00，避免影响高峰下载。

清理范围：

- Docker/OCI：按 Registry、namespace、镜像 tag、digest、最近访问时间清理。
- SteamCMD：按 AppID、Depot、最近访问时间、版本更新时间清理。
- 通用资源：按域名、URL 前缀、文件类型、最近访问时间清理。
- 临时文件：失败下载、未完成分片、过期任务日志。

清理规则：

- LRU：优先清理最长时间未访问的缓存。
- TTL：清理超过指定保留天数的缓存。
- 容量回收：持续清理直到低于目标水位线，例如从 90% 回收到 75%。
- 低命中清理：优先清理命中次数低于阈值的对象。
- 模块配额：每个模块可设置最大容量，超额后只清理该模块。
- 永久保留：被用户 pin 的镜像、AppID、URL 永不自动清理。

任务执行要求：

- 执行前生成 dry-run 预估，显示预计删除对象数和释放容量。
- 支持「仅预演不删除」。
- 支持手动立即执行一次。
- 支持暂停、恢复、删除任务。
- 支持执行日志与清理明细。
- 支持失败重试和失败告警。
- 清理时必须避免删除正在下载或正在被客户端读取的文件。

默认任务建议：

| 任务 | 默认规则 | 说明 |
|---|---|---|
| 每日临时文件清理 | 每天 03:30 | 清理失败下载、过期分片、任务临时目录 |
| 容量水位清理 | 总占用超过 85% | 按 LRU 清理到 75% |
| 旧日志清理 | 每天 04:00 | 详细请求日志保留 7 天，聚合指标保留 30 天 |
| 低命中缓存清理 | 每周日 04:30 | 清理 30 天未访问且未 pin 的对象 |

### 9.6.4 小容量 VPS 优化策略

开启「小容量 VPS 优化」后，缓存系统的第一目标是保证服务器不被磁盘、内存和后台任务拖垮，其次才是最大化命中率。关闭该开关时，系统使用标准缓存策略，不应在 UI 或原型中表现为另一套产品。

核心策略：

- 容量硬上限：缓存总量不得超过用户设置上限，推荐默认取 `min(可用磁盘 * 50%, 20GB)`。
- 系统保底空间：始终保留 5GB 或 15% 可用空间，取更大值。
- 单对象大小限制：超过阈值的大文件只转发不落盘，避免一个 Steam/模型文件吃满磁盘。
- 分模块配额：Docker、Steam、Resource、Generic 分别限额，Docker 优先级最高。
- 写入前检查：开始缓存前预估对象大小，无法确认大小时按保守策略处理。
- 边下边清：写入新对象前若空间不足，先执行快速 LRU 清理，再决定是否落盘。
- 只读旁路：磁盘低于安全水位时停止写缓存，但代理服务继续转发请求。
- 小索引策略：元数据索引保留必要字段，日志和统计降采样，减少 SQLite/磁盘膨胀。
- 预热限制：开启优化后默认限制 Steam AppID、Hugging Face 模型等超大预热任务。

推荐设置值：

| 配置项 | 建议默认值 | 说明 |
|---|---:|---|
| 缓存总上限 | `min(可用磁盘 * 50%, 20GB)` | 用户可手动覆盖 |
| Docker 配额 | 缓存总上限的 70%-80% | Docker 优先 |
| Steam 配额 | 默认 0-2GB | 大文件建议外接存储 |
| Resource 配额 | 默认 1-2GB | GitHub/浏览器二进制等可按需增加 |
| Generic 配额 | 默认 1GB | 避免通用资源挤占主缓存 |
| 最大单对象落盘 | 1GB | 可选 256MB/512MB/2GB/自定义 |
| 清理触发水位 | 80% | 超过后自动清理 |
| 清理目标水位 | 60% | 清理到目标水位后停止 |
| 详细日志保留 | 3 天 | 标准策略可保留 7 天 |
| 聚合指标保留 | 14 天 | 标准策略可保留 30 天 |

用户提示要求：

- 当用户启用 SteamCMD 或 AI 模型缓存时，如果「小容量 VPS 优化」已开启，必须提示预计磁盘风险。
- 创建预热任务时必须显示预计占用与当前剩余额度。
- 当某个对象因超过单对象限制被旁路时，请求日志需标记为 `BYPASS_SIZE_LIMIT`。
- 当系统进入只读旁路状态时，首页和缓存页必须显示明显状态。

### 9.6.5 防止误删

所有清理操作需要二次确认，并显示预计释放容量。

---

## 9.7 访问控制与安全

### 9.7.1 控制台安全

- 首次启动强制设置管理员密码。
- 支持登录会话过期。
- 支持修改密码。
- 支持基础审计日志。
- 支持限制控制台监听地址。

### 9.7.2 代理访问控制

默认策略：

- 内网部署：允许内网网段访问。
- 公网部署：建议开启访问 Token 或 IP 白名单。

功能要求：

- 支持按客户端 IP 白名单。
- 支持 Basic Auth / Bearer Token。
- 支持为 Docker mirror 生成只读访问凭据。
- 支持限制单 IP 并发和速率。
- 支持黑名单封禁异常请求。

### 9.7.3 上游凭据管理

对 Docker Hub 私有凭据、GHCR Token 等敏感信息：

- 默认不要求配置。
- 仅在用户主动启用认证上游时保存。
- 本地加密存储。
- 控制台不回显完整凭据。
- 日志必须脱敏。

---

## 9.8 监控与日志

### 9.8.1 仪表盘指标

| 指标 | 说明 |
|---|---|
| 总请求数 | 所有代理请求 |
| 成功率 | 2xx/3xx 请求占比 |
| 缓存命中率 | HIT / MISS / BYPASS |
| 上游延迟 | 各 Registry/Steam 上游耗时 |
| 下载流量 | 入站/出站流量 |
| 缓存写入速度 | 当前写入吞吐 |
| 错误分布 | DNS、连接、上游、权限、磁盘等 |
| 客户端排行 | 按 IP/Token 统计 |

### 9.8.2 请求日志

字段：

- 时间。
- 客户端 IP。
- 模块：Docker/Steam/Generic。
- 目标上游。
- 资源标识：镜像名、digest、AppID、URL path。
- HTTP 状态码。
- 缓存状态：HIT/MISS/BYPASS/ERROR。
- 响应大小。
- 耗时。
- 错误原因。

### 9.8.3 日志保留

- 默认保留 7 天详细日志。
- 聚合指标保留 30 天。
- 支持手动导出诊断包。

---

## 10. 用户交互路径（User Flows）

## 10.1 首次部署与初始化流程

```text
用户运行安装命令
  ↓
服务启动成功，终端输出 Web 地址
  ↓
用户打开 Web 控制台
  ↓
设置管理员账号密码
  ↓
选择部署模式：IP / 域名 TLS / 内网网关
  ↓
配置缓存目录与容量上限
  ↓
选择启用模块：Docker、SteamCMD 等
  ↓
系统执行环境自检
  ↓
生成首份 Docker 客户端配置
  ↓
用户复制配置并重启 Docker
  ↓
执行 docker pull nginx:latest 验证
  ↓
控制台显示请求日志与缓存 MISS/HIT
```

## 10.2 Docker 加速配置流程

```text
进入「客户端配置」页面
  ↓
选择运行环境：Docker Engine
  ↓
选择目标系统：Ubuntu/Debian/CentOS/macOS
  ↓
选择镜像源：Docker Hub
  ↓
系统生成 daemon.json 和命令
  ↓
用户复制执行
  ↓
用户点击「我已配置，开始验证」
  ↓
系统提示执行 docker pull hello-world
  ↓
控制台捕获请求日志
  ↓
验证成功：显示加速已生效
  ↓
验证失败：进入诊断页面，输出原因和修复建议
```

## 10.3 containerd / Kubernetes 配置流程

```text
进入「客户端配置」页面
  ↓
选择运行环境：containerd / k3s / rke2
  ↓
选择需要加速的 Registry：docker.io、registry.k8s.io、quay.io
  ↓
系统生成 hosts.toml 或 registries.yaml
  ↓
用户复制到节点
  ↓
重启 containerd/k3s/rke2
  ↓
执行 crictl pull registry.k8s.io/pause:3.9
  ↓
系统通过日志检测请求是否命中
  ↓
显示验证结果
```

## 10.4 镜像预热流程

```text
进入「预热任务」页面
  ↓
选择「Docker/OCI 镜像预热」
  ↓
粘贴镜像列表
  ↓
系统识别 Registry 和 tag
  ↓
用户确认任务
  ↓
任务进入队列
  ↓
系统下载 manifest 和 layers
  ↓
实时显示进度、速度、失败重试
  ↓
任务完成，展示缓存占用和可用配置
```

## 10.5 SteamCMD 加速启用流程

```text
进入「SteamCMD 加速」页面
  ↓
点击启用 Steam 缓存模块
  ↓
系统启动 DNS + 内容缓存服务
  ↓
系统检测 53/80/443 等端口占用情况
  ↓
用户选择使用方式：内网 DNS / Docker --dns / 手动 DNS
  ↓
系统生成配置和验证命令
  ↓
用户运行 nslookup 验证 Steam 域名解析
  ↓
用户运行 SteamCMD 下载 AppID
  ↓
控制台显示 Steam 请求日志
  ↓
首次下载为 MISS，后续下载为 HIT
```

## 10.6 Steam AppID 预热流程

```text
进入「SteamCMD 加速」页面
  ↓
点击「新增 AppID」
  ↓
输入 AppID 或选择内置模板
  ↓
选择登录方式：anonymous / 用户自行登录
  ↓
系统生成 SteamCMD 命令
  ↓
用户可选择在本机执行，或使用内置预热 Worker
  ↓
系统记录下载请求与缓存命中
  ↓
展示 AppID 缓存占用与最近更新时间
```

## 10.7 下载失败诊断流程

```text
用户发现 docker pull / steamcmd 下载失败
  ↓
进入「诊断中心」
  ↓
选择诊断类型：Docker / SteamCMD / DNS / 磁盘 / 上游
  ↓
输入镜像名、AppID 或粘贴日志
  ↓
系统运行检查：DNS、端口、上游、权限、缓存目录、日志
  ↓
输出诊断结论、原因、修复步骤、验证命令
  ↓
用户执行修复
  ↓
再次验证
```

## 10.8 资源加速中心配置流程

```text
进入「资源加速中心」
  ↓
选择资源类型：GitHub / Playwright / Terraform / Helm / Hugging Face
  ↓
配置白名单：repo、模型名、域名、版本规则或文件模式
  ↓
系统检查当前存储策略、小容量 VPS 优化开关和预计占用
  ↓
若已开启小容量 VPS 优化且资源过大，提示旁路或外接存储
  ↓
系统生成接入配置与验证命令
  ↓
用户复制到 CI、开发机或服务器
  ↓
系统捕获请求日志并显示 HIT/MISS/BYPASS 原因
  ↓
验证成功后可创建预热或定时更新任务
```

## 10.9 小容量 VPS 优化保护流程

```text
系统检测缓存写入请求
  ↓
检查小容量 VPS 优化开关、剩余空间、模块配额、单对象大小
  ↓
对象超过限制？
  ├── 是：流式转发，不落盘，日志标记 BYPASS_SIZE_LIMIT
  └── 否：继续
  ↓
剩余空间低于安全水位？
  ├── 是：执行快速 LRU 清理
  └── 否：继续
  ↓
清理后仍不足？
  ├── 是：进入只读旁路状态并告警
  └── 否：写入缓存
  ↓
更新缓存指标、请求日志和清理建议
```

---

## 11. 页面清单与跳转关系

### 11.1 页面总览

| 页面 | 路径 | 目标 |
|---|---|---|
| 登录页 | `/login` | 管理员登录 |
| 初始化向导 | `/setup` | 首次配置系统 |
| 首页仪表盘 | `/dashboard` | 查看整体状态 |
| Docker 加速 | `/docker` | 管理 Docker/OCI 代理 |
| Registry 详情 | `/docker/registries/:id` | 查看单个 Registry 状态 |
| SteamCMD 加速 | `/steamcmd` | 管理 Steam 缓存和 AppID |
| 客户端配置 | `/clients` | 生成 Docker/containerd/SteamCMD 配置 |
| 预热任务 | `/preheat` | 创建和查看预热任务 |
| 资源加速中心 | `/resources` | 管理 GitHub、AI 模型、测试浏览器、云原生工具等白名单资源缓存 |
| 缓存管理 | `/cache` | 查看和清理缓存 |
| 请求日志 | `/logs` | 搜索代理请求日志 |
| 诊断中心 | `/diagnostics` | 排查下载失败 |
| 安全设置 | `/security` | 登录、Token、白名单 |
| 系统设置 | `/settings` | 域名、TLS、存储、更新 |
| 关于/帮助 | `/help` | 内置文档和版本信息 |

### 11.2 页面跳转关系

```text
/login
  └── /setup（首次登录）
        └── /dashboard

/dashboard
  ├── /docker
  │     └── /docker/registries/:id
  ├── /steamcmd
  ├── /clients
  ├── /preheat
  ├── /resources
  ├── /cache
  ├── /logs
  ├── /diagnostics
  ├── /security
  ├── /settings
  └── /help

/docker
  ├── /clients?type=docker
  ├── /preheat?type=docker
  └── /diagnostics?type=docker

/steamcmd
  ├── /clients?type=steamcmd
  ├── /preheat?type=steam
  └── /diagnostics?type=steamcmd

/resources
  ├── /clients?type=resource
  ├── /preheat?type=resource
  └── /diagnostics?type=resource

/logs
  └── /diagnostics?fromLog=<log_id>
```

---

## 12. 页面详细需求

## 12.1 登录页

### 元素

- 产品 Logo 与名称。
- 用户名输入框。
- 密码输入框。
- 登录按钮。
- 忘记密码/重置提示。
- 当前服务版本。

### 规则

- 密码错误 5 次后短时间限制登录。
- 登录成功进入仪表盘或初始化向导。
- 不展示具体用户是否存在。

---

## 12.2 初始化向导

### 步骤 1：管理员设置

- 设置用户名。
- 设置密码。
- 确认密码。
- 显示密码强度。

### 步骤 2：部署模式

选项：

- 本机/内网 IP 模式。
- 公网域名 + TLS 模式。
- 反向代理后方模式。

### 步骤 3：缓存策略

选项：

- 标准缓存策略：默认策略，适合大多数 VPS/NAS/服务器。
- 小容量 VPS 优化：设置开关，开启后限制缓存上限、单对象落盘和日志保留。
- 自定义策略：手动设置缓存上限、保底空间、单对象大小、清理水位和模块配额。

系统可根据磁盘空间给出建议，例如可用空间较小时提示「建议开启小容量 VPS 优化」，但不强制切换项目形态。

### 步骤 4：缓存配置

- 缓存根目录。
- 最大占用容量。
- 自动清理阈值。
- 保留策略。
- 是否启用大文件旁路。

### 步骤 5：启用模块

- Docker Hub 加速：默认启用。
- 多 Registry 加速：可选。
- SteamCMD 加速：可选；若开启小容量 VPS 优化，大文件按设置旁路或阻止落盘。
- 资源加速中心：可选，默认只启用 GitHub/测试浏览器配置生成。
- 通用下载加速：默认关闭。

### 步骤 6：环境自检

检查：

- 上游连接。
- DNS 解析。
- 端口监听。
- 磁盘读写。
- 容器状态。
- 系统时间。

### 步骤 7：生成配置

展示 Docker Engine 快速配置，并提供复制按钮。

---

## 12.3 首页仪表盘

### 核心卡片

- 服务状态。
- 当前缓存占用。
- 今日请求数。
- 今日缓存命中率。
- 上游健康状态。
- 资源加速状态：GitHub、测试浏览器、AI 模型、云原生工具。
- 小容量 VPS 优化状态：开关状态、保底空间、旁路次数。
- 最近错误数。

### 图表

- 24 小时请求趋势。
- Docker/Steam/Generic 流量占比。
- 缓存命中率趋势。
- 上游延迟趋势。

### 快捷操作

- 生成 Docker 配置。
- 新建预热任务。
- 启用 SteamCMD 加速。
- 配置 GitHub/Playwright/Terraform/Hugging Face 加速。
- 打开诊断中心。

---

## 12.4 Docker 加速页

### 列表字段

- Registry 名称。
- 上游地址。
- 本地加速地址。
- 状态。
- 今日请求数。
- 缓存命中率。
- 缓存占用。
- 最近错误。

### 操作

- 启用/停用。
- 复制加速地址。
- 生成客户端配置。
- 查看日志。
- 运行健康检查。
- 清理缓存。

### Registry 详情

展示：

- 基础信息。
- 上游健康。
- 缓存策略。
- 最近请求。
- 错误分布。
- 预热历史。

---

## 12.5 SteamCMD 加速页

### 状态区域

- Steam 缓存模块状态。
- DNS 服务状态。
- 内容缓存服务状态。
- Steam 域名规则版本。
- 当前缓存占用。
- 最近 24 小时命中率。

### AppID 管理

字段：

- AppID。
- 名称。
- 登录方式。
- 缓存占用。
- 最近请求。
- 命中率。
- 操作。

操作：

- 新增 AppID。
- 使用模板。
- 生成 SteamCMD 命令。
- 预热。
- 诊断。
- 清理缓存。

### 配置向导

选项：

- Linux 服务器 DNS 配置。
- Docker `--dns` 配置。
- Docker Compose 配置。
- 路由器/DHCP 下发 DNS。

---

## 12.6 客户端配置页

### 表单项

- 使用场景：Docker / containerd / k3s / rke2 / SteamCMD / GitHub / Playwright / Puppeteer / Terraform / Helm / Hugging Face。
- 操作系统。
- 加速模块。
- 地址类型：IP / 域名。
- 是否需要认证。

### 输出区

- 配置文件内容。
- 一键命令。
- 验证命令。
- 回滚命令。
- 注意事项。

### 交互要求

- 所有配置支持一键复制。
- 复制后显示成功提示。
- 危险操作用醒目提示。
- 支持导出配置包。

---

## 12.7 预热任务页

### 任务类型

- Docker/OCI 镜像预热。
- Steam AppID 预热。
- 通用 URL 预热。

### 任务状态

- 等待中。
- 运行中。
- 成功。
- 部分成功。
- 失败。
- 已取消。

### 详情展示

- 任务输入。
- 当前进度。
- 下载速度。
- 已缓存大小。
- 失败项与错误原因。
- 重试次数。
- 日志。

---

## 12.8 资源加速中心页

### 模块卡片

页面必须以卡片形式展示各资源类型：

- GitHub：Release、Raw、LFS、codeload。
- 测试浏览器：Playwright、Puppeteer、Cypress、WebDriver。
- 云原生工具：Terraform、Helm、kubectl、k9s、kind、k3s。
- AI 模型：Hugging Face 模型、datasets、tokenizer、PyTorch wheel。
- 语言包管理器：npm、pip、Go、Maven、Rust、Homebrew。
- 游戏 Mod：Modrinth、CurseForge、PaperMC、Fabric、Forge。

### 每个模块展示字段

- 模块状态：启用 / 未启用 / 旁路 / 异常。
- 白名单数量。
- 今日请求数。
- 缓存命中率。
- 当前缓存占用。
- 小容量 VPS 风险等级。
- 最近错误。

### 核心操作

- 新增白名单规则。
- 生成接入配置。
- 创建预热任务。
- 运行上游健康检查。
- 查看请求日志。
- 发起诊断。
- 进入缓存清理。

### 资源规则创建

创建资源规则时必须收集：

- 资源类型。
- 白名单范围：repo、模型名、域名、路径前缀、文件模式。
- 缓存 TTL。
- 最大单对象大小。
- 小容量 VPS 优化开启后行为：缓存 / 旁路 / 阻止。
- 是否允许预热。
- 是否允许带 query 的 URL 缓存。

### 页面状态要求

- 空态：展示推荐模板，例如 GitHub Release、Playwright、Terraform Provider。
- 风险态：小容量 VPS 下启用 AI/Steam 大文件时必须高亮风险。
- 错误态：上游超时、checksum 不匹配、权限不足、URL 不在白名单时要能跳转诊断。
- 成功态：展示配置复制、验证命令和最近一次命中日志。

---

## 12.9 缓存管理页

### 视图

- 按模块查看。
- 按 Registry 查看。
- 按镜像 namespace 查看。
- 按 Steam AppID 查看。
- 按最近访问时间查看。

### 操作

- 清理选中项。
- 设置保留。
- 刷新 manifest。
- 导出缓存报告。
- 设置自动清理规则。
- 新建定时清理任务。
- 查看清理任务执行历史。
- 运行 dry-run 预估释放容量。

### 定时清理区域

页面必须提供「定时清理任务」独立区域：

- 展示任务名称、触发方式、清理范围、目标水位线、下次执行时间、最近执行结果。
- 支持新增任务、编辑任务、暂停任务、立即执行、查看日志。
- 创建任务时提供模板：每日临时文件清理、容量水位清理、低命中缓存清理、旧日志清理。
- 执行前展示 dry-run 结果：预计释放容量、预计删除对象数、受影响模块、被保护对象数。
- 对危险规则给出提示，例如「清理所有 7 天未访问 Steam 内容」可能导致下次 SteamCMD 重新下载。

---

## 12.10 请求日志页

### 筛选条件

- 时间范围。
- 模块。
- 资源类型：Docker、Steam、GitHub、AI 模型、测试浏览器、Terraform/Helm、Generic。
- Registry/上游。
- 缓存状态。
- HTTP 状态码。
- 客户端 IP。
- 关键词。

### 操作

- 查看详情。
- 从日志发起诊断。
- 导出日志。
- 加入黑名单。

---

## 12.11 诊断中心

### 诊断类型

- Docker 拉取失败。
- containerd/K8s 拉取失败。
- SteamCMD 下载失败。
- GitHub Release / Raw 下载失败。
- Playwright / Puppeteer / Cypress 二进制下载失败。
- Terraform Provider / Helm Chart 拉取失败。
- Hugging Face 模型下载失败。
- 小容量 VPS 旁路或磁盘水位异常。
- DNS 未生效。
- 上游不可达。
- 磁盘/权限问题。
- TLS/证书问题。

### 诊断输出

每次诊断输出：

- 结论。
- 严重程度。
- 证据。
- 原因解释。
- 修复步骤。
- 验证命令。
- 相关日志链接。

---

## 13. 数据对象模型

### 13.1 RegistryConfig

| 字段 | 类型 | 说明 |
|---|---|---|
| id | string | Registry 配置 ID |
| name | string | 显示名称 |
| type | enum | dockerhub / ghcr / quay / k8s / gcr / custom |
| upstreamUrl | string | 上游地址 |
| publicEndpoint | string | 对外加速地址 |
| enabled | boolean | 是否启用 |
| authMode | enum | none / upstreamCredential / clientToken |
| cacheTTL | number | manifest 缓存时间 |
| maxCacheSize | number | 最大缓存容量 |
| createdAt | datetime | 创建时间 |
| updatedAt | datetime | 更新时间 |

### 13.2 CacheObject

| 字段 | 类型 | 说明 |
|---|---|---|
| id | string | 缓存对象 ID |
| module | enum | docker / steam / resource / generic |
| source | string | 上游来源 |
| key | string | 镜像 digest、Steam path 或 URL |
| size | number | 文件大小 |
| hitCount | number | 命中次数 |
| lastAccessAt | datetime | 最近访问时间 |
| createdAt | datetime | 首次缓存时间 |
| pinned | boolean | 是否保留 |

### 13.3 CleanupTask

| 字段 | 类型 | 说明 |
|---|---|---|
| id | string | 清理任务 ID |
| name | string | 任务名称 |
| enabled | boolean | 是否启用 |
| triggerType | enum | cron / capacity / manual |
| cronExpr | string | Cron 表达式 |
| capacityThreshold | number | 触发水位线，例如 0.85 |
| targetWatermark | number | 清理目标水位线，例如 0.75 |
| modules | array | 作用模块：docker / steam / generic / logs / temp |
| strategy | enum | lru / ttl / lowHit / mixed |
| ttlDays | number | 保留天数 |
| minHitCount | number | 最小命中次数 |
| dryRunBeforeDelete | boolean | 是否执行前预演 |
| lastRunAt | datetime | 最近执行时间 |
| nextRunAt | datetime | 下次执行时间 |
| lastResult | enum | success / partial / failed / skipped |
| releasedBytes | number | 最近释放容量 |
| protectedCount | number | 最近被 pin 保护的对象数 |

### 13.4 CapacityOptimizationConfig

| 字段 | 类型 | 说明 |
|---|---|---|
| enabled | boolean | 是否启用小容量 VPS 优化 |
| autoRecommend | boolean | 是否根据磁盘空间自动推荐开启 |
| diskRecommendThreshold | number | 推荐开启的磁盘阈值，例如 40GB |
| cacheMaxSize | number | 开启优化后的缓存总上限 |
| minFreeSpace | number | 系统保底空间 |
| maxObjectSize | number | 最大单对象落盘大小 |
| moduleQuotas | json | Docker/Steam/Resource/Generic 分模块配额 |
| largeObjectBehavior | enum | cache / bypass / block |
| readonlyBypassThreshold | number | 进入只读旁路状态的剩余空间阈值 |
| logRetentionDays | number | 详细日志保留天数 |
| metricsRetentionDays | number | 聚合指标保留天数 |

### 13.5 ResourceRule

| 字段 | 类型 | 说明 |
|---|---|---|
| id | string | 资源规则 ID |
| type | enum | github / browser / terraform / helm / huggingface / package / gameMod / custom |
| name | string | 显示名称 |
| enabled | boolean | 是否启用 |
| whitelist | json | repo、模型名、域名、路径前缀、文件模式等白名单规则 |
| cacheTTL | number | 缓存有效期 |
| maxObjectSize | number | 最大单对象落盘大小 |
| smallVpsBehavior | enum | inherit / cache / bypass / block | 小容量 VPS 优化开启时的行为 |
| allowPreheat | boolean | 是否允许预热 |
| allowQueryCache | boolean | 是否允许带 query URL 缓存 |
| authRequired | boolean | 是否需要上游凭据 |
| lastHealthStatus | enum | healthy / degraded / failed / unknown |
| createdAt | datetime | 创建时间 |
| updatedAt | datetime | 更新时间 |

### 13.6 SteamApp

| 字段 | 类型 | 说明 |
|---|---|---|
| id | string | 记录 ID |
| appId | string | Steam AppID |
| name | string | 名称 |
| loginMode | enum | anonymous / userRequired |
| installCommand | string | 命令模板 |
| cacheSize | number | 缓存占用 |
| lastWarmAt | datetime | 最近预热时间 |
| hitRate | number | 命中率 |

### 13.7 PreheatTask

| 字段 | 类型 | 说明 |
|---|---|---|
| id | string | 任务 ID |
| type | enum | docker / steam / resource / generic |
| status | enum | pending / running / success / partial / failed / canceled |
| input | json | 输入内容 |
| progress | number | 进度百分比 |
| speed | number | 当前速度 |
| result | json | 结果摘要 |
| error | string | 错误信息 |
| createdAt | datetime | 创建时间 |
| finishedAt | datetime | 完成时间 |

### 13.8 RequestLog

| 字段 | 类型 | 说明 |
|---|---|---|
| id | string | 日志 ID |
| time | datetime | 请求时间 |
| clientIp | string | 客户端 IP |
| module | enum | docker / steam / resource / generic |
| method | string | HTTP 方法 |
| host | string | 请求域名 |
| path | string | 请求路径 |
| status | number | HTTP 状态码 |
| cacheStatus | enum | HIT / MISS / BYPASS / ERROR |
| bytesSent | number | 响应大小 |
| durationMs | number | 耗时 |
| errorReason | string | 错误原因 |

---

## 14. API 需求概览

### 14.1 认证

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/api/auth/login` | 登录 |
| POST | `/api/auth/logout` | 登出 |
| GET | `/api/auth/me` | 当前用户 |
| POST | `/api/auth/change-password` | 修改密码 |

### 14.2 仪表盘

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/dashboard/summary` | 总览指标 |
| GET | `/api/dashboard/timeseries` | 趋势数据 |
| GET | `/api/health` | 系统健康 |

### 14.3 Registry

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/registries` | Registry 列表 |
| POST | `/api/registries` | 新增 Registry |
| PATCH | `/api/registries/:id` | 修改配置 |
| POST | `/api/registries/:id/check` | 健康检查 |
| POST | `/api/registries/:id/clear-cache` | 清理缓存 |

### 14.4 SteamCMD

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/steam/status` | Steam 模块状态 |
| POST | `/api/steam/enable` | 启用模块 |
| GET | `/api/steam/domains` | 域名规则列表 |
| POST | `/api/steam/domains/update` | 更新域名规则 |
| GET | `/api/steam/apps` | AppID 列表 |
| POST | `/api/steam/apps` | 新增 AppID |
| POST | `/api/steam/apps/:id/command` | 生成命令 |

### 14.5 客户端配置

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/api/client-config/docker` | 生成 Docker 配置 |
| POST | `/api/client-config/containerd` | 生成 containerd 配置 |
| POST | `/api/client-config/k3s` | 生成 k3s 配置 |
| POST | `/api/client-config/steamcmd` | 生成 SteamCMD 配置 |
| POST | `/api/client-config/resource` | 生成资源加速配置 |
| POST | `/api/client-config/export` | 导出配置包 |

### 14.6 预热任务

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/preheat/tasks` | 任务列表 |
| POST | `/api/preheat/tasks` | 创建任务 |
| GET | `/api/preheat/tasks/:id` | 任务详情 |
| POST | `/api/preheat/tasks/:id/cancel` | 取消任务 |
| POST | `/api/preheat/tasks/:id/retry` | 重试任务 |

### 14.7 诊断

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/api/diagnostics/docker` | Docker 诊断 |
| POST | `/api/diagnostics/containerd` | containerd 诊断 |
| POST | `/api/diagnostics/steamcmd` | SteamCMD 诊断 |
| POST | `/api/diagnostics/resource` | GitHub/AI/测试浏览器/云原生资源诊断 |
| POST | `/api/diagnostics/dns` | DNS 诊断 |
| POST | `/api/diagnostics/bundle` | 导出诊断包 |

### 14.8 定时清理任务

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/cleanup/tasks` | 清理任务列表 |
| POST | `/api/cleanup/tasks` | 创建清理任务 |
| PATCH | `/api/cleanup/tasks/:id` | 修改清理任务 |
| POST | `/api/cleanup/tasks/:id/enable` | 启用任务 |
| POST | `/api/cleanup/tasks/:id/disable` | 暂停任务 |
| POST | `/api/cleanup/tasks/:id/dry-run` | 预演清理并返回预计释放容量 |
| POST | `/api/cleanup/tasks/:id/run` | 立即执行一次 |
| GET | `/api/cleanup/tasks/:id/runs` | 查看执行历史 |
| GET | `/api/cleanup/runs/:runId` | 查看单次执行明细 |

### 14.9 小容量 VPS 优化设置

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/storage/optimization` | 获取小容量 VPS 优化开关与策略 |
| PATCH | `/api/storage/optimization` | 更新优化开关、缓存上限、保底空间和旁路策略 |
| POST | `/api/storage/optimization/recommend` | 根据磁盘空间生成推荐设置 |
| POST | `/api/storage/check` | 检查磁盘空间、保底空间和旁路状态 |
| POST | `/api/storage/estimate` | 预估预热任务或模块启用后的占用风险 |

### 14.10 资源加速中心

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/resources/rules` | 资源白名单规则列表 |
| POST | `/api/resources/rules` | 新增资源白名单规则 |
| PATCH | `/api/resources/rules/:id` | 修改资源规则 |
| POST | `/api/resources/rules/:id/check` | 检查资源上游健康 |
| POST | `/api/resources/rules/:id/preheat` | 基于规则创建预热任务 |
| POST | `/api/resources/rules/:id/estimate` | 预估规则缓存占用与小盘风险 |
| POST | `/api/resources/templates` | 获取推荐模板：GitHub/Playwright/Terraform/HF 等 |
| POST | `/api/resources/resolve` | 解析用户输入 URL/模型名/repo 并匹配规则 |

---

## 15. 非功能性需求

## 15.1 性能

- 已缓存 blob / content 响应应接近本地磁盘读取性能。
- 单实例至少支持 50 个并发下载连接。
- 控制台接口 P95 响应时间 <= 300ms。
- 大文件下载采用流式转发，不一次性读入内存。
- 小容量 VPS 优化下，单进程常驻内存目标 <= 256MB，空闲状态 CPU 接近 0。
- 对超过单对象限制的资源必须支持旁路转发，不得因为无法缓存而中断用户下载。
- 缓存写入必须支持断点和异常清理，避免损坏文件被命中。

## 15.2 可用性

- 核心代理模块异常不应导致 Web 控制台完全不可用。
- 上游不可达时返回明确错误，不静默卡住。
- 服务重启后缓存索引可恢复。
- 预热任务可恢复或标记失败重试。
- 磁盘低于安全水位时自动进入只读旁路模式，代理转发不中断。
- 资源规则失效时只影响对应资源类型，不影响 Docker Hub 主链路。

## 15.3 安全

- 管理接口默认需要登录。
- 敏感配置加密存储。
- 日志脱敏 Token、Authorization、Cookie。
- 默认不开放通用 HTTP 代理能力。
- 所有资源加速必须基于白名单规则，默认拒绝任意 URL 代理。
- 公网部署时提示开启访问控制。
- 不执行用户输入的任意 shell，预热任务需做参数白名单。

## 15.4 兼容性

- 支持 Docker Engine 20.10+。
- 支持 containerd 1.6+。
- 支持 k3s/rke2 常见版本。
- 支持 Linux SteamCMD。
- Web 控制台支持 Chrome、Edge、Safari 近两年版本。

## 15.5 可观测性

- 所有代理请求有结构化日志。
- 支持 Prometheus metrics（P1）。
- 支持导出诊断包。
- 错误必须分类，而不是只显示 500。

## 15.6 可维护性

- 模块化设计，Docker、Steam、Generic 互不强耦合。
- 上游域名规则、Registry 配置可在线更新。
- 配置文件可备份和迁移。
- 版本升级提供变更说明和回滚建议。

## 15.7 国际化

- 首发 UI 和文档为简体中文。
- 配置 key、API 字段保留英文。
- 后续可支持英文界面。

---

## 16. 推荐系统架构

> 该章节用于帮助研发理解产品边界，不强制限定最终实现。

```text
客户端 Docker / containerd / SteamCMD
          ↓
   CNCacheHub Gateway
          ↓
 ┌─────────────────────────────┐
 │  Web 控制台 / API Server     │
 │  Auth / Config / Diagnostics │
 └─────────────────────────────┘
          ↓
 ┌───────────────┬───────────────┬───────────────┬───────────────┐
 │ Docker Proxy  │ Steam Cache   │ Resource Cache│ Generic Cache │
 │ OCI Registry  │ DNS + HTTP    │ Whitelist     │ URL Whitelist │
 └───────────────┴───────────────┴───────────────┴───────────────┘
          ↓
 ┌─────────────────────────────┐
 │ Cache Store / Metadata DB    │
 │ Filesystem + SQLite/Postgres │
 └─────────────────────────────┘
          ↓
 Docker Hub / GHCR / Quay / Steam CDN / 其他上游
```

### 16.1 组件建议

| 组件 | 说明 |
|---|---|
| Gateway | 统一入口，负责路由、TLS、访问控制、日志 |
| Registry Proxy | OCI pull-through cache，可多实例支持不同上游 |
| Steam Cache | DNS 服务 + 内容缓存服务，兼容 LANCache 思路 |
| Resource Cache | GitHub、AI 模型、测试浏览器、Terraform/Helm 等白名单资源缓存 |
| API Server | Web 控制台、配置生成、任务管理、诊断 |
| Worker | 预热任务、健康检查、清理任务 |
| Metadata DB | 保存配置、任务、日志索引、缓存元信息 |
| File Cache | 保存 blob、Steam 内容、通用资源 |

---

## 17. 关键规则与边界条件

### 17.1 Docker 规则

- Docker Hub mirror 是 P0，必须稳定。
- 多 Registry 不一定能通过 Docker daemon 的 `registry-mirrors` 一把梭解决，需要给 containerd/K8s 生成更精准配置。
- `latest` tag 不应永久缓存，必须有 TTL 或手动刷新。
- digest 内容可长期缓存。
- 私有镜像不是首发核心；支持时必须保护凭据。

### 17.2 SteamCMD 规则

- SteamCMD 加速效果依赖 DNS 是否指向 CNCacheHub。
- 单机首次下载不一定更快，核心收益是重复下载和多机器共享。
- 不保证所有 Steam 内容都可缓存。
- 需要明确区分 anonymous 可下载和需要账号授权的 AppID。
- 不保存用户 Steam 账号密码，除非未来设计专门的本地凭据保险箱且默认关闭。

### 17.3 资源加速规则

- 资源加速中心必须白名单优先，用户不能默认代理任意 URL。
- GitHub raw / release 资源必须有 TTL，避免长期返回过期安装脚本。
- Hugging Face / AI 模型等大文件必须先做空间预估，小容量 VPS 默认旁路。
- Playwright / Puppeteer / Cypress 必须优先提供环境变量配置生成和 CI 验证命令。
- Terraform Provider / Helm Chart 必须保留 checksum 校验语义，不允许为了命中缓存跳过完整性验证。
- 带 token、签名、session、临时 query 的 URL 默认不缓存，日志必须脱敏。

### 17.4 小容量 VPS 规则

- 启用优化后，Docker 代理链路优先级最高，Steam/AI/通用大文件不得抢占 Docker 配额。
- 任何对象写入前必须检查单对象大小、模块配额、总容量上限和系统保底空间。
- 进入只读旁路模式时，代理转发继续工作，但停止写入新缓存。
- 所有旁路都必须在请求日志中给出原因，例如 `BYPASS_SIZE_LIMIT`、`BYPASS_LOW_DISK`、`BYPASS_RULE`。
- 预热任务必须在创建前给出预计占用，超出配额时默认阻止。

### 17.5 通用代理规则

- 默认关闭通用代理，防止被当作开放代理滥用。
- 只允许白名单域名。
- 不缓存带敏感 query/token 的 URL，除非显式规则允许并脱敏。

---

## 18. 异常与错误提示

### 18.1 Docker 常见错误

| 错误 | 判断方式 | 用户提示 |
|---|---|---|
| 上游超时 | 连接 Docker Hub 超时 | 上游连接不稳定，请稍后重试或更换部署线路 |
| rate limit | Docker Hub 返回限流 | 当前匿名请求被限流，可配置 Docker Hub Token 或等待恢复 |
| 配置未生效 | 控制台无请求日志 | Docker 客户端可能未使用 mirror，请检查 daemon.json 并重启 Docker |
| TLS 错误 | 客户端证书校验失败 | 域名证书不匹配或自签证书未信任 |
| 磁盘不足 | 写缓存失败 | 缓存目录空间不足，请清理或扩容 |
| 小盘旁路 | 对象超过单文件缓存上限 | 当前为小容量 VPS 优化，大文件已转发但不落盘，避免磁盘被占满 |

### 18.2 SteamCMD 常见错误

| 错误 | 判断方式 | 用户提示 |
|---|---|---|
| DNS 未生效 | Steam 域名未解析到 CNCacheHub | 请将 SteamCMD 客户端 DNS 指向 CNCacheHub |
| AppID 权限不足 | SteamCMD 日志出现 no subscription | 该 AppID 需要账号授权，anonymous 不可下载 |
| CDN 连接慢 | 上游延迟高 | 首次下载仍依赖上游，后续命中缓存会加速 |
| 缓存未命中 | HIT 率低 | 内容可能版本更新或请求路径不同，请检查 AppID 和 depot |
| 磁盘不足 | 写入失败 | 请清理 Steam 缓存或扩大缓存目录 |

---

## 19. 验收标准

### 19.1 P0 验收

- 在全新 Debian 12 服务器上可通过 Docker Compose 成功启动。
- 首次打开控制台能完成初始化。
- 能生成 Docker daemon.json。
- 配置客户端后能成功 `docker pull nginx:latest`。
- 第二次拉取同一镜像时控制台显示缓存命中。
- 能查看请求日志、缓存占用、上游健康。
- 在 10-20GB 小容量 VPS 优化下，默认缓存上限、最大单对象落盘、只读旁路和保底空间策略生效。
- 大文件超过限制时请求可成功转发，日志标记 `BYPASS_SIZE_LIMIT`，且不写入缓存。
- 能配置至少一个默认定时清理任务，并支持 dry-run 预估释放容量。
- 能清理 Docker 缓存。
- 控制台必须有登录保护。
- 所有关键文案为简体中文。

### 19.2 P1 验收

- 支持至少 4 个 Registry：Docker Hub、GHCR、Quay、registry.k8s.io。
- 能生成 containerd/k3s 配置。
- SteamCMD 模块能启动 DNS 和缓存服务。
- SteamCMD Docker 示例能让请求进入 CNCacheHub。
- 同一 Steam AppID 第二次下载能观察到缓存命中。
- 预热任务可创建、运行、失败重试。
- 诊断中心能识别 Docker 配置未生效、DNS 未生效、磁盘不足三类问题。
- 资源加速中心支持至少 4 类 P1 资源：GitHub Release/Raw、Playwright/Puppeteer、Terraform/Helm、Hugging Face。
- 资源规则必须支持白名单、空间预估、小容量 VPS 优化旁路和配置生成。
- Playwright 或 Puppeteer 配置生成后，用户能通过环境变量完成浏览器二进制下载验证。

### 19.3 P2 验收

- 支持通用白名单 URL 缓存。
- 支持 Prometheus metrics。
- 支持多节点缓存。
- 支持导出完整诊断包。
- 支持访问 Token 和 IP 白名单策略。

---

## 20. 发布计划

### 20.1 Milestone 1：MVP（Docker 加速可用）

周期：2-3 周

范围：

- Docker Compose 部署。
- Web 初始化。
- 小容量 VPS 优化开关与推荐设置。
- Docker Hub pull-through cache。
- Docker daemon.json 生成。
- 请求日志。
- 缓存占用展示。
- 定时清理任务与 dry-run。
- 基础健康检查。

交付目标：个人开发者可以真正用它拉 Docker Hub 镜像。

### 20.2 Milestone 2：多 Registry + 诊断 + 资源加速首批

周期：2-3 周

范围：

- GHCR、Quay、registry.k8s.io。
- containerd/k3s 配置生成。
- 镜像预热任务。
- 诊断中心第一版。
- GitHub Release/Raw 白名单缓存。
- Playwright/Puppeteer/Cypress 二进制配置生成与缓存。
- Terraform Provider / Helm Chart 缓存基础能力。
- Hugging Face 模型规则、空间预估和可选优化旁路。
- Token/IP 白名单。

交付目标：小团队、Kubernetes 用户、CI 测试环境和 AI 部署场景可以接入。

### 20.3 Milestone 3：SteamCMD 加速

周期：3-4 周

范围：

- Steam DNS 模块。
- Steam 内容缓存。
- Steam AppID 管理。
- SteamCMD Docker 示例生成。
- SteamCMD 日志诊断。
- Steam 缓存管理。

交付目标：游戏服主可以通过 DNS + 缓存复用 SteamCMD 下载内容。

### 20.4 Milestone 4：扩展下载源和高级能力

周期：持续迭代

范围：

- npm / pnpm / pip / Go / Maven / Rust / Homebrew 可选缓存。
- Prometheus metrics。
- 多节点缓存。
- 配置包导出。

---

## 21. 风险与应对

| 风险 | 影响 | 应对 |
|---|---|---|
| Docker Hub 策略变化 | 代理失效或限流 | 保持上游适配层，提供凭据配置和错误诊断 |
| 多 Registry 配置复杂 | 用户配置失败 | 通过配置生成器和验证命令降低门槛 |
| Steam 内容缓存不稳定 | 加速效果不确定 | 明确边界，优先支持 DNS 可控和可缓存内容 |
| 公网开放被滥用 | 带宽消耗和安全风险 | 默认提示访问控制，支持 Token/IP 白名单/限速 |
| 磁盘快速占满 | 服务不可用 | 容量阈值、LRU 清理、告警 |
| HTTPS 缓存受限 | 通用下载效果有限 | 不做 MITM，使用白名单和合法可缓存资源 |
| 新手不理解 DNS | SteamCMD 接入失败 | 提供 Docker --dns 模式和诊断中心 |
| 资源加速变成开放代理 | 安全与带宽风险 | 白名单规则、访问控制、默认拒绝任意 URL |
| AI/Steam 大文件吃满磁盘 | VPS 服务不可用 | 小容量 VPS 优化开关、单对象限制、空间预估、外接存储建议 |
| GitHub raw 缓存过期 | 安装脚本执行旧版本 | raw 文件短 TTL、手动刷新、命中日志显示版本/ETag |
| Terraform/Helm 校验失败 | IaC 初始化失败 | 保留 checksum 校验语义，不跳过完整性检查 |

---

## 22. 运营与文档需求

### 22.1 内置文档

必须内置以下中文文档：

- 5 分钟快速开始。
- Docker Engine 接入。
- containerd/k3s 接入。
- SteamCMD 加速接入。
- 公网部署安全建议。
- 小容量 VPS 部署与省盘配置。
- GitHub Release / Raw 加速接入。
- Playwright / Puppeteer / Cypress 下载加速接入。
- Terraform Provider / Helm Chart 加速接入。
- Hugging Face / AI 模型缓存与小容量 VPS 优化说明。
- 缓存清理与磁盘管理。
- 常见问题排查。

### 22.2 示例模板

- Docker Hub 基础配置。
- Kubernetes 常用镜像配置。
- SteamCMD 常见 AppID 命令。
- Docker Compose 运行 SteamCMD 示例。
- Playwright / Puppeteer / Cypress 环境变量示例。
- Terraform provider mirror 示例。
- Helm repo proxy 示例。
- Hugging Face 模型预热示例。
- 反向代理 Nginx/Caddy 示例。

---

## 23. 首版默认配置建议

```yaml
server:
  listen: "0.0.0.0:8080"
  public_url: "http://<server-ip>:8080"

cache:
  root: "/var/lib/cncachehub/cache"
  max_size: "100GB"
  min_free_space: "5GB"
  max_object_size: "0"  # 0 表示标准策略下不限制单对象大小
  cleanup_threshold: 0.85
  default_policy: "lru"
  bypass_large_objects: false

small_vps_optimization:
  enabled: false
  auto_recommend: true
  disk_recommend_threshold: "40GB"
  max_size: "min(available_disk * 0.5, 20GB)"
  min_free_space: "5GB"
  max_object_size: "1GB"
  readonly_bypass_free_space: "2GB"
  large_object_behavior: "bypass" # cache / bypass / block
  module_quota:
    docker: "80%"
    steam: "0GB-2GB"
    resource: "1GB-2GB"
    generic: "1GB"
  logging_retention: "3d"
  metrics_retention: "14d"

registry:
  dockerhub:
    enabled: true
    upstream: "https://registry-1.docker.io"
    endpoint: "/v2/"
    manifest_ttl: "6h"
    blob_ttl: "720h"

steam:
  enabled: false
  dns_listen: "0.0.0.0:53"
  cache_listen: "0.0.0.0:80"
  upstream_dns: ["223.5.5.5", "119.29.29.29", "1.1.1.1"]

resources:
  enabled: true
  default_policy: "whitelist_only"
  deny_arbitrary_url: true
  strip_sensitive_query: true
  modules:
    github:
      enabled: true
      raw_ttl: "10m"
      release_asset_ttl: "720h"
    browser_binaries:
      enabled: true
      max_versions_per_tool: 3
    terraform_helm:
      enabled: true
      keep_checksum_validation: true
    huggingface:
      enabled: false
      small_vps_default: "bypass_large_objects"

resource_rules:
  max_object_size_default: "1GB"
  cache_signed_url: false
  require_whitelist: true
  dry_run_before_preheat: true

auth:
  console_login_required: true
  proxy_access_control: "private_network"

logging:
  request_log_retention: "7d"
  metrics_retention: "30d"

cleanup:
  enabled: true
  dry_run_before_delete: true
  temp_files:
    cron: "30 3 * * *"
    ttl: "24h"
  capacity_guard:
    threshold: 0.85
    target_watermark: 0.75
    strategy: "lru"
  low_hit_cache:
    cron: "30 4 * * 0"
    ttl: "30d"
    min_hit_count: 2
```

---

## 24. 原型阶段建议

后续原型建议优先做 PC Web 控制台，因为该产品核心使用场景是部署管理、配置复制、日志诊断和缓存监控，PC 端更适合展示复杂配置与表格。

建议原型页面：

1. 登录页。
2. 初始化向导。
3. 首页仪表盘。
4. Docker 加速页。
5. SteamCMD 加速页。
6. 资源加速中心页。
7. 客户端配置生成页。
8. 预热任务页。
9. 诊断中心。
10. 缓存管理页。
11. 请求日志页。

视觉风格建议：

- 极客暗黑 + 高级灰。
- 品牌色使用低饱和青绿色或电光紫，仅用于关键状态和 CTA。
- 信息密度偏 B 端，但避免传统后台的拥挤感。
- 重点突出「复制配置」「验证生效」「诊断修复」「容量优化开关」四个动作。

原型状态覆盖要求：

- 首页必须展示资源加速中心状态和小容量 VPS 优化开关状态。
- 资源加速中心必须覆盖空态、启用态、风险态、错误态。
- 缓存管理页必须展示定时清理、dry-run、小容量 VPS 优化开关和只读旁路状态。
- 诊断中心必须覆盖 Docker、SteamCMD、GitHub、Playwright、Terraform/HF、磁盘水位。
- 客户端配置页必须支持 Docker、containerd、SteamCMD、Playwright/Puppeteer、Terraform/Helm 至少五类输出。
- 所有危险操作必须有二次确认或明显风险提示。

---

## 25. AI/研发执行提示词与生成约束

该章节用于把 PRD 转化为后续 AI coding、UI 原型、接口设计、测试用例时的统一输入，避免不同执行者理解偏差。

### 25.1 通用执行提示词

```text
你是一名资深全栈产品研发 Agent，正在实现 CNCacheHub：一个面向国内网络环境的自托管资源缓存与下载加速网关。

你的目标：在不破坏安全边界的前提下，实现 Docker/OCI 镜像加速、SteamCMD 缓存、资源加速中心、小容量 VPS 保护、配置生成、诊断中心和缓存可观测能力。

必须遵守：
1. Docker Hub 公共镜像代理是 P0 主线，任何扩展功能不得影响它的稳定性。
2. 默认禁止开放代理；所有 GitHub/HF/Playwright/Terraform 等资源必须基于白名单规则。
3. 不绕过 Steam、GitHub、Hugging Face 等平台授权，不保存敏感账号密码，日志必须脱敏。
4. 小容量 VPS 必须优先保命：保底空间、单对象限制、模块配额、只读旁路必须生效。
5. 每个配置生成结果必须包含复制内容、验证命令、回滚方式和诊断入口。
6. 每个下载/代理请求必须产生结构化日志：来源、目标、状态、缓存命中、旁路原因、错误原因。
7. 所有危险操作必须 dry-run 或二次确认：清理缓存、预热大文件、启用公网访问、关闭访问控制。

输出要求：
- 先说明实现范围和不做范围。
- 给出核心数据模型、API、页面交互和验收用例。
- 对小容量 VPS、权限、安全、缓存一致性单独列出处理方案。
- 不要生成与 PRD 冲突的开放代理、明文凭据、跳过校验、绕过授权方案。
```

### 25.2 原型生成提示词

```text
基于 CNCacheHub PRD 生成 PC 端高保真 Web 控制台原型。

技术限制：纯 HTML5 + Tailwind CSS CDN + Vanilla JS，不使用 React/Vue，不需要构建工具。

必须包含页面：登录页、初始化向导、首页仪表盘、Docker 加速、SteamCMD 加速、资源加速中心、客户端配置、预热任务、诊断中心、缓存管理、请求日志、系统设置。

视觉要求：极客暗黑 + 高级灰，低饱和青绿色/电光紫作为 CTA，现代 SaaS 控制台，大圆角、柔和阴影、毛玻璃、hover 动效，全部中文文案。

交互要求：
- 侧边栏可切换页面。
- 配置代码块支持复制反馈。
- 诊断按钮能展示结果状态。
- 缓存管理支持 dry-run 预览。
- 小容量 VPS 优化要可见：设置开关、推荐开启条件、缓存上限、单对象限制、旁路原因。
- 资源加速中心要展示 GitHub、Playwright、Terraform/Helm、Hugging Face 的启用/风险/错误状态。

自检：不得出现 Lorem Ipsum、占位图、英文按钮、默认 Bootstrap 风格；所有链接可点击，所有页面可访问。
```

### 25.3 接口与测试生成提示词

```text
请根据 CNCacheHub PRD 生成后端 API 设计和测试用例。

重点模块：auth、dashboard、registries、steam、resources、client-config、preheat、cleanup、storage、diagnostics、logs。

测试必须覆盖：
- Docker Hub 拉取成功、缓存命中、上游失败、rate limit。
- 小容量 VPS：对象超限旁路、低磁盘只读旁路、清理到目标水位线。
- 资源加速中心：白名单命中、非白名单拒绝、签名 URL 不缓存、GitHub raw TTL、Playwright 配置生成。
- SteamCMD：DNS 未生效、anonymous 权限不足、缓存命中、端口冲突。
- 安全：Token 脱敏、访问控制、危险操作 dry-run。

输出格式：接口列表、请求响应示例、错误码、测试矩阵、验收命令。
```

### 25.4 内部评估摘要

- **精准度**：PRD 已从单一 Docker/Steam 加速扩展为「自托管资源加速中心」，仍保持 Docker P0 主线。
- **完整性**：补齐了 GitHub、AI 模型、测试浏览器、Terraform/Helm、小容量 VPS、定时清理、配置生成和诊断闭环。
- **效率性**：后续执行可以直接按模块拆分，不需要重新解释产品边界。
- **鲁棒性**：通过白名单、旁路、dry-run、脱敏、保底空间等规则降低滥用和爆盘风险。

---

## 26. 总结

CNCacheHub 的核心不是再造一个普通代理，而是把国内开发、AI 部署、自动化测试、云原生初始化和游戏服务器部署中最烦人的慢下载问题产品化：

- 对 Docker 用户：给一个能自托管、可观测、可诊断的镜像加速入口。
- 对 Kubernetes 用户：降低多 Registry、Terraform、Helm 和云原生工具配置难度。
- 对 SteamCMD 用户：用 DNS + 缓存解决重复下载慢的问题，同时保留授权边界。
- 对 AI/测试用户：提供 Hugging Face、Playwright、Puppeteer、Cypress 等高频大文件资源加速。
- 对小容量 VPS 用户：通过可选优化开关、大文件旁路、保底空间和定时清理避免磁盘爆炸。
- 对小团队：把带宽、缓存、安全、配置、诊断统一管理起来。

首版应坚持「Docker 加速可用 + 小盘安全」作为硬目标，再扩展 SteamCMD 和资源加速中心。只要配置生成器、诊断中心、缓存可视化和安全边界做扎实，这个项目会比纯脚本或单个 registry mirror 更有长期价值。
