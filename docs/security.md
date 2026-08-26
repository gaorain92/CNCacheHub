# CNCacheHub 部署安全指南

> 适用于 v0.1.x 及以上版本。本文档面向**自托管场景** — 你把 CNCacheHub 部署到自己的 VPS / NAS / 内网服务器上。
> 如果部署到公网，强烈建议读完所有章节。

---

## 1. 安全模型

CNCacheHub 是一个**自托管的反向代理缓存**。它的安全边界是：

```
[ 客户端 / 浏览器 / docker pull / steamcmd ]
            ↓ HTTPS (推荐) 或 HTTP
[ Caddy 反代 (80/443) ]
            ↓
[ cncachehub-server :8080 ] ← 攻击面
            ↓
[ 公网上游 (registry-1.docker.io 等) ]
```

**威胁来源**：
1. 暴露在公网的 80/443 端口 — DoS、暴力破解、漏洞扫描
2. Caddy 之前的 TLS 终结（如果你用 Let's Encrypt / 自签）
3. 容器逃逸 — 攻击者拿到 server shell 后想逃出容器
4. 数据泄露 — `/metrics`、访问日志、admin 凭据
5. 凭据泄露 — `ADMIN_PASSWORD` 出现在 `docker inspect`、env vars、日志

**CNCacheHub 已经做了**（v0.1.x）：

| 项 | 状态 | 说明 |
|---|---|---|
| SQL 注入 | ✅ | 所有 SQL 走 `?` 占位符，无字符串拼接 |
| XSS | ✅ | 前端不写 `innerHTML`，所有数据用 v-text/插值 |
| 鉴权 | ✅ | Cookie + CSRF，所有 admin API 走会话校验 |
| 速率限制 | ✅ | Go server 中间件，token-bucket 防爆破 + DoS |
| 5xx 信息泄露 | ✅ | 错误信息脱敏，不暴露内部路径 / SQL |
| RBAC | ✅ | admin / 普通用户 两级（IsAdmin bool） |
| Docker 加固 | ✅ | cap_drop ALL + no-new-privileges + mem/pid 限制 + read_only |
| Caddy 加固 | ✅ | body 大小限制 + `/metrics` IP 白名单 + 严格响应头 |
| 路径校验 | ✅ | `validate_path` 拒绝 `/` `/tmp` `..` 等危险路径 |
| 多发行版 | ✅ | `install.sh` 自动识别 apt / dnf / yum / pacman / apk |

**用户必须自己做的**（本文档剩余部分）：

| 项 | 严重度 | 章节 |
|---|---|---|
| 防火墙 | 🔴 必做 | §3 |
| SSH 硬化 | 🔴 必做 | §4 |
| 管理员密码 | 🔴 必做 | §5 |
| TLS | 🟡 公网强烈建议 | §6 |
| 自动更新 | 🟡 强烈建议 | §7 |
| 备份 | 🟡 强烈建议 | §8 |
| Docker 凭据 | 🟡 重要 | §9 |
| 审计日志 | 🟢 可选 | §10 |

---

## 2. 默认端口与暴露面

部署完成后，**默认只暴露 80 端口**（或 80+443，如果你走 Let's Encrypt）。

| 端口 | 用途 | 是否需要公网 |
|---|---|---|
| 80 | Caddy HTTP（自动重定向到 HTTPS 如果有） | ✅ 是 |
| 443 | Caddy HTTPS（仅 letsencrypt 模式） | ✅ 是 |
| 8080 | Go server 内部端口 — **不暴露** | ❌ 否（仅 Caddy 反代到） |
| 22 | SSH — 你自己管理 | ✅ 是（但要硬化，见 §4） |

> 验证命令（在你的 VPS 上跑）：
> ```bash
> ss -tlnp | grep -E ':(80|443|8080|22) '
> # 应该看到 80/443 公网监听；8080 应该只 listen 127.0.0.1
> # 22 看你的 SSH 端口
> ```

---

## 3. 防火墙（🔴 必做）

### 3.1 ufw（Debian/Ubuntu 推荐）

```bash
sudo apt install ufw
sudo ufw default deny incoming
sudo ufw default allow outgoing

# SSH — 改你的端口
sudo ufw allow 22/tcp comment "SSH"

# Caddy
sudo ufw allow 80/tcp comment "Caddy HTTP"
sudo ufw allow 443/tcp comment "Caddy HTTPS"

# 启用
sudo ufw enable
sudo ufw status verbose
```

### 3.2 firewalld（CentOS/RHEL/Fedora）

```bash
sudo firewall-cmd --permanent --add-service=ssh
sudo firewall-cmd --permanent --add-service=http
sudo firewall-cmd --permanent --add-service=https
sudo firewall-cmd --reload
sudo firewall-cmd --list-all
```

### 3.3 iptables（所有发行版通用）

```bash
# 默认策略
sudo iptables -P INPUT DROP
sudo iptables -P FORWARD DROP
sudo iptables -P OUTPUT ACCEPT

# loopback
sudo iptables -A INPUT -i lo -j ACCEPT

# 已建立的连接
sudo iptables -A INPUT -m state --state ESTABLISHED,RELATED -j ACCEPT

# SSH
sudo iptables -A INPUT -p tcp --dport 22 -j ACCEPT

# Caddy
sudo iptables -A INPUT -p tcp --dport 80 -j ACCEPT
sudo iptables -A INPUT -p tcp --dport 443 -j ACCEPT

# 保存（发行版命令不同）
sudo apt install iptables-persistent && sudo netfilter-persistent save
# 或
sudo service iptables save
```

### 3.4 云厂商安全组

如果你用阿里云 / 腾讯云 / AWS / Vultr / DigitalOcean 等，**一定要在云控制台也开安全组**。VPS 内的防火墙只是第一层，云厂商的安全组是第二层。

- 阿里云 ECS：ECS 控制台 → 安全组 → 入方向规则
- 腾讯云 CVM：安全组 → 入站规则
- AWS EC2：EC2 → Security Groups → Inbound Rules

---

## 4. SSH 硬化（🔴 必做）

### 4.1 改 SSH 端口（防扫描器骚扰）

```bash
sudo vi /etc/ssh/sshd_config
# Port 22  →  Port 2222（选个不常见的高位端口）
sudo systemctl restart sshd
```

### 4.2 禁 root 登录

```bash
# /etc/ssh/sshd_config
PermitRootLogin no                # 禁用 root 直接登录
PasswordAuthentication no         # 禁用密码登录（用密钥）
PubkeyAuthentication yes
AllowUsers deploy                 # 只允许特定用户
```

### 4.3 用密钥登录

```bash
# 在你本地
ssh-keygen -t ed25519 -C "your-laptop"
ssh-copy-id -i ~/.ssh/id_ed25519.pub deploy@your-server
```

### 4.4 fail2ban（防 SSH 爆破）

```bash
sudo apt install fail2ban
sudo systemctl enable --now fail2ban
# 默认配置已能挡大部分；想要更严就 /etc/fail2ban/jail.local 自定义
```

---

## 5. 管理员密码（🔴 必做）

### 5.1 部署时设强密码

`install.sh` express 模式默认随机生成 22 字符密码；interactive 模式会问。**别设 `admin` `password` `12345678`** — 弱密码会被 Go server 的强度校验拒绝。

### 5.2 定期轮换

- 至少每 90 天换一次
- 换完同步更新 `/opt/cncachehub/deploy/generated/.env` 然后 `docker compose up -d`

### 5.3 别复用其他站点密码

CNCacheHub 的密码存在 SQLite 里（bcrypt），不是明文。但**别让一个站的失守影响其他站**。

### 5.4 用密码管理器

1Password / Bitwarden / KeePass。随机密码人脑记不住。

---

## 6. TLS（🟡 公网强烈建议）

### 6.1 用 install.sh 一键配 Let's Encrypt

```bash
./scripts/install.sh --mode=expert \
  --tls-mode=letsencrypt \
  --domain=cache.your-domain.com \
  --admin-email=you@your-domain.com
```

Caddy 会自动：
- 申请证书（DNS 必须先指向你的服务器）
- 强制 HTTPS（301 redirect from :80）
- 每 60 天自动续期

### 6.2 自签证书（仅内网）

```bash
./scripts/install.sh --mode=expert --tls-mode=self-signed
```

浏览器会警告"不安全" — 客户端 docker / steamcmd 需要加 `--insecure-registry` 跳过校验。

### 6.3 仅 HTTP（仅开发 / 局域网）

```bash
./scripts/install.sh --mode=express --tls-mode=off
```

⚠️ **不推荐公网** — 任何明文流量都能被中间人攻击，包括管理员会话 cookie。

---

## 7. 自动安全更新（🟡 强烈建议）

### 7.1 unattended-upgrades（Debian/Ubuntu）

```bash
sudo apt install unattended-upgrades apt-listchanges
sudo dpkg-reconfigure -plow unattended-upgrades

# /etc/apt/apt.conf.d/50unattended-upgrades
Unattended-Upgrade::Allowed-Origins {
    "${distro_id}:${distro_codename}-security";
};
Unattended-Upgrade::Automatic-Reboot "false";
```

### 7.2 dnf-automatic（RHEL/Fedora）

```bash
sudo dnf install dnf-automatic
sudo systemctl enable --now dnf-automatic.timer
# /etc/dnf/automatic.conf
upgrade_type = security
```

### 7.3 CNCacheHub 自身的更新

```bash
# 在你部署目录
./scripts/install.sh update --mode=express
```

会重新 `git pull` + 重新 build 镜像 + restart。**做这个前先备份 DB**（§8）。

---

## 8. 备份（🟡 强烈建议）

### 8.1 备份什么

- SQLite DB — `cncachehub_data` volume 里的 `cncachehub.db`
- 缓存 blobs — `cncachehub_cache` volume（可以重建，但 20GB 重新拉慢）
- `.env` — `deploy/generated/.env`（含 ADMIN_PASSWORD）
- 上游凭据（如果用了）— 在 SQLite 里，但要确保 DB 备份在

### 8.2 简单 cron 备份

```bash
# /opt/cncachehub/scripts/backup.sh
#!/usr/bin/env bash
set -euo pipefail
BACKUP_DIR="/var/backups/cncachehub"
DATE=$(date -u +"%Y%m%dT%H%M%SZ")
mkdir -p "$BACKUP_DIR"

# DB
docker compose -f /opt/cncachehub/deploy/generated/docker-compose.yml stop server
docker run --rm \
  -v cncachehub_cncachehub_data:/data:ro \
  -v "$BACKUP_DIR":/backup \
  alpine sh -c "cp /data/cncachehub.db /backup/cncachehub-${DATE}.db && \
                cp /data/cncachehub.db-wal /backup/ 2>/dev/null || true && \
                cp /data/cncachehub.db-shm /backup/ 2>/dev/null || true"
docker compose -f /opt/cncachehub/deploy/generated/docker-compose.yml start server

# .env（部署配置 + 密码）
cp /opt/cncachehub/deploy/generated/.env "$BACKUP_DIR/env-${DATE}.bak"

# 留 7 天
find "$BACKUP_DIR" -type f -mtime +7 -delete
```

```bash
chmod +x /opt/cncachehub/scripts/backup.sh
# 每天凌晨 3 点跑
echo "0 3 * * * root /opt/cncachehub/scripts/backup.sh" | sudo tee /etc/cron.d/cncachehub-backup
```

### 8.3 异地备份

把 `/var/backups/cncachehub` 同步到：
- 另一台 VPS（rsync over SSH）
- 对象存储（S3 / 阿里云 OSS / 腾讯云 COS）
- 本地 NAS

```bash
# rsync 到另一台 VPS
rsync -avz --delete /var/backups/cncachehub/ backup@backup-server:/backups/cncachehub/
```

### 8.4 恢复

```bash
# 1. 停服
docker compose -f /opt/cncachehub/deploy/generated/docker-compose.yml down

# 2. 恢复 DB
docker run --rm \
  -v cncachehub_cncachehub_data:/data \
  -v /var/backups/cncachehub:/backup:ro \
  alpine sh -c "cp /backup/cncachehub-YYYYMMDD.db /data/cncachehub.db"

# 3. 起服
docker compose -f /opt/cncachehub/deploy/generated/docker-compose.yml up -d
```

---

## 9. Docker 凭据（🟡 重要）

### 9.1 ADMIN_PASSWORD 暴露面

`ADMIN_PASSWORD` 通过 env var 传给容器 — **任何能进容器的人**都能看到。任何能 `docker inspect` 容器的人也能看到。

**缓解**：
1. 容器只跑 CNCacheHub 自己的服务，不混其他东西
2. 防火墙挡 `2375`/`2376`（Docker daemon 端口）— 绝对不能公网暴露
3. 定期换密码（§5.2）

### 9.2 Docker socket 隔离

CNCacheHub **不需要** docker socket 挂载 — 它的 server 只缓存 HTTP 请求，不编排其他容器。

如果你看到任何 `volumes: - /var/run/docker.sock:/var/run/docker.sock` 的写法，**立刻删掉** — 那是 backdoor 级别的暴露。

### 9.3 named volumes 不是 host 路径

```yaml
volumes:
  - cncachehub_data:/var/lib/cncachehub
```

`cncachehub_data` 是 Docker 管理的 named volume，**不存在于你主机的 `/var/lib/cncachehub`**。备份要走 `docker run --rm -v cncachehub_data:/data`（§8.2）。

---

## 10. 审计日志（🟢 可选）

CNCacheHub 把请求日志写到 `/var/log/cncachehub/access.log`（容器里）。

```bash
# 看实时
docker compose -f /opt/cncachehub/deploy/generated/docker-compose.yml logs -f server

# 走 syslog（如果有 rsyslog）
# /etc/rsyslog.d/cncachehub.conf
module(load="imfile")
input(type="imfile"
      File="/var/lib/docker/volumes/cncachehub_cncachehub_data/_data/log/access.log"
      Tag="cncachehub"
      Severity="info")
```

---

## 11. 加固检查清单

部署前对照打勾：

- [ ] SSH 改端口 + 禁密码 + 禁 root
- [ ] 防火墙开 SSH + 80/443
- [ ] ADMIN_PASSWORD 是 22 字符随机（`install.sh` 自动）
- [ ] 数据目录在 `/opt` 或 `/srv` 或 `/home`，不在 `/tmp` / `/var` 根
- [ ] （公网）TLS 走 Let's Encrypt
- [ ] （公网）云厂商安全组也开
- [ ] 自动安全更新配好（`unattended-upgrades` / `dnf-automatic`）
- [ ] 每日 cron 备份到异地
- [ ] `/metrics` 只能内网访问（Caddy 已默认这样配）
- [ ] `cncachehub-server` 容器的 `docker inspect` 只暴露 `8080`，没绑 `0.0.0.0`

部署后验证：

```bash
# 端口扫描
nmap -p 1-65535 your-server-ip
# 期望只看到 22 (或你改的)、80、443

# Docker 加固验证
docker compose -f /opt/cncachehub/deploy/generated/docker-compose.yml config | \
  grep -E "(cap_drop|no-new-privileges|read_only|mem_limit)"

# 证书有效期
echo | openssl s_client -connect your-domain.com:443 -servername your-domain.com 2>/dev/null | \
  openssl x509 -noout -dates

# 密码强度
echo "$ADMIN_PASSWORD" | awk 'length($0) >= 12 { print "OK" } length($0) < 12 { print "TOO SHORT" }'
```

---

## 12. 报告漏洞

发现安全问题：
- GitHub Issues: 不适合 — 公开可见
- 邮件: `security@cncachehub.dev`（待设）
- 微信群: 见 README

我们承诺 90 天内修复 critical 漏洞。

---

## 13. 参考

- [OWASP Docker Top 10](https://owasp.org/www-project-docker-top-10/)
- [CIS Docker Benchmark](https://www.cisecurity.org/benchmark/docker)
- [Caddy Security Headers](https://caddyserver.com/docs/caddyfile/directives/header)
- [Docker security best practices](https://docs.docker.com/engine/security/)
