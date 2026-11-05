# Security Policy

## 支持的版本

以下版本会收到安全修复：

| 版本 | 受支持 |
|------|--------|
| 当前版本线（见 `wails.json` 的 `info.productVersion`） | ✅ 是 |
| 更早的版本 | ❌ 否 |

## 报告漏洞

请勿在公开 Issue 中披露安全漏洞。请通过以下方式私下报告：

- 在 GitHub 上创建 [Security Advisory](https://github.com/sky-jiangcheng/repotory/security/advisories/new)（推荐）
- 或向维护者发送包含漏洞细节的私信/邮件

请在报告中包含：

1. 漏洞描述与影响
2. 复现步骤（含版本与平台）
3. 可选的 PoC
4. 建议的修复方案（可选）

## 响应时间

- **24 小时内**：确认收到报告并启动评估
- **5 个工作日内**：给出漏洞定级与修复计划
- **高危/严重漏洞**：优先修复，通常在发布后 72 小时内出修复版本

## 安全设计

Cinder 在开发中遵循以下安全原则：

- 所有数据库查询使用参数化语句，防止 SQL 注入
- 对传入 `git log` 命令的参数（date/author/branch）进行正则格式校验
- API 层限制请求体最大 1MB
- 客户端统一错误消息，内部错误详情仅记录在服务端日志
- 配置键白名单（仅允许写入 `daily_code_standard` 与 `scan_depth`）
- 前端渲染 Markdown 前经过 DOMPurify 消毒

### HTTP 安全响应头

无头服务 `repotory-server`（`cmd/server`，默认 `127.0.0.1:18765`）在所有响应中注入以下安全头：
注意它是**独立二进制**，不是桌面应用内嵌的服务器——桌面应用本身不监听任何 HTTP 端口，全仓唯一的 `ListenAndServe` 就在 `cmd/server`。

| 响应头 | 值 | 用途 |
|--------|-----|------|
| `Content-Security-Policy` | `default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: https:; font-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'` | 防止 XSS 与点击劫持 |
| `X-Content-Type-Options` | `nosniff` | 禁止 MIME 嗅探 |
| `X-Frame-Options` | `DENY` | 禁止页面被嵌入 iframe |
| `Referrer-Policy` | `strict-origin-when-cross-origin` | 控制 Referrer 泄露 |
| `Permissions-Policy` | `camera=(), microphone=(), geolocation=(), acceleration=()` | 禁用敏感权限 |

> `unsafe-inline` 仅用于 `style-src`，以支持 CSS-in-JS 与 Tailwind 工具类。无 `unsafe-eval`，不执行动态代码。

## 依赖安全

仓库启用 Dependabot 每月扫描 Go modules、npm 依赖与 GitHub Actions，发现漏洞会自动创建 PR。请及时合并相关升级。

### 远程访问安全

无头服务与开发模式（`scripts/dev.sh`）都只绑 `127.0.0.1:18765`，默认仅本机可访问。如需通过局域网访问，建议：

- 在防火墙层限制端口访问范围
- 或使用 SSH 隧道：`ssh -L 18765:localhost:18765 user@remote-host`

## 范围

- 仓库内代码与配置
- GitHub Actions 工作流（`/.github/workflows/`）
- 前端构建产物中的依赖

不在范围内：用户自行安装的第三方 Git 插件与未经仓库分发的二进制。
