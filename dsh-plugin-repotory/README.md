# dsh-plugin-repotory

DeepSeek Harness (`dsh`) 插件：把 Cinder 本地的 git 分析能力暴露给 Harness 作为
三个模型可见的工具（Tool）。所有分析逻辑都在 Cinder 共享的 Go service 层
（`internal/service`），本插件只是薄客户端，**桌面 App 与 Harness 共用同一份代码，零逻辑复制**。

## 架构

```
Cinder 桌面 App  ─┐
                    ├─→ internal/service（单一事实源）
dsh-plugin-repotory ─┴─→ Headless HTTP 服务 (repotory server) ─→ SQLite/FTS5
```

- 桌面 App：保持独立，走 Wails 绑定，完全不动。
- `repotory server`：Go 编写的 headless HTTP 服务，复用 `internal/service`。
- 本插件：注册 3 个 Tool，调用 localhost 上的 headless 服务；可自启并托管其生命周期。

## 提供的工具

| Tool | 对应 Cinder 能力 | 说明 |
|---|---|---|
| `repotory_ai_context` | Copy AI Context / llms.txt | 返回整库 AI 可读上下文（项目目录、技术栈、README 摘要、知识笔记） |
| `repotory_repo_overview` | 项目概览 | 技术栈 / 语言分布 / 依赖 / 主要贡献者 / 活跃度(Heatmap) / 最近提交 |
| `repotory_search` | FTS5 知识检索 | 跨笔记/Todo 的全文搜索 |

## 构建 headless 服务

```bash
# 在 Cinder 仓库根目录
GOPROXY=https://goproxy.cn,direct GOSUMDB=off CGO_ENABLED=0 \
  go build -o cinder-server ./cmd/server
```

## 配置（环境变量）

| 变量 | 默认 | 说明 |
|---|---|---|
| `REPOTORY_HTTP_PORT` | `18765` | headless 服务端口（仅绑 127.0.0.1） |
| `REPOTORY_SERVER_BIN` | 空 | headless 服务二进制路径；设置后插件自启它 |
| `REPOTORY_AUTOSTART` | `1` | 设为 `0` 关闭自启（需你手动启动服务） |

## 依赖与类型检查

依赖锁在 `package-lock.json` 里，用 **npm** 装（`npm ci`）。早先的清单写的是
`"@deepseek-ai/dsh-tools": "*"`，而该包在 npm 上只有 `0.0.1-rc.*` 预发布版——`*` 跟着
`latest` 走，下一次谁发布什么就是什么，没有锁文件也没有任何 CI 看过这个文件能否编译。
现在：

```bash
npm ci            # 按锁文件装（与 CI 同一条命令）
npm run typecheck  # tsc --noEmit
```

插件**没有构建产物**：`main` 直接指向 `src/index.ts`，dsh 自己加载 TypeScript。所以
这道门问的不是「能不能打包」，而是「这个文件还对着它调用的 API 类型检查得过吗」。
CI 的 `dsh-plugin-test` job 就是这两条命令。

## 安装到 dsh

方式一（推荐，外置插件）：

```bash
# 先装好依赖
cd dsh-plugin-repotory && npm ci

# 注册到 web profile
pnpm dsh plugin --profile web add /abs/path/to/dsh-plugin-repotory

# 启动并设置环境变量
export REPOTORY_SERVER_BIN=/abs/path/to/cinder-server
pnpm dsh web
```

方式二（patch 调试）：用仓库内的 `cordis.yml` 直接 `--patch`：

```bash
export REPOTORY_SERVER_BIN=/abs/path/to/cinder-server
pnpm dsh web --patch ./dsh-plugin-repotory/cordis.yml
```

## 手动冒烟测试（无需 dsh）

启动 headless 服务后，用 curl 验证插件调用的同一批端点：

```bash
REPOTORY_HTTP_PORT=18765 ./cinder-server &
curl -s http://127.0.0.1:18765/health
curl -s -X POST http://127.0.0.1:18765/api/ai_context
curl -s "http://127.0.0.1:18765/api/search?q=deploy"
curl -s http://127.0.0.1:18765/api/project/1/overview
```
