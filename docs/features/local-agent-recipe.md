---
title: 本地 Agent 配方（全离线）
order: 8
---

# 本地 Agent 配方：让完全离线的模型用上记忆层

Repotory 的 MCP server 是一个 **stdio 进程**：它不关心对面是哪个模型、哪个客户端，也不联网。所以「完全离线的本地 Agent + Repotory」在技术上一直成立——缺的只是一页接线说明和一次真机验证。这页补上这两样。

一句话版本：**本地模型跑在你机器上，知识库也在你机器上，两边用 MCP 在进程间通信，全程没有一条出网请求。**

## 谁负责什么

| 角色 | 干什么 | 不干什么 |
|------|--------|----------|
| Repotory（`repotory-mcp`） | 存上下文、检索、写交接笔记 | 不提供模型、不代持密钥、不联网 |
| 本地模型（ollama / LM Studio …） | 推理、决定调用哪个工具 | 不存持久状态（模型本身没有记忆） |
| MCP 客户端（Claude Code / Cursor / opencode …） | 把两者连起来：注入工具、转发调用 | 不解释语义 |

对话历史在模型进程退出时就没了；**跨会话记住的东西只能来自 Repotory**。这就是这层数据底座在你离线时唯一不可替代的原因。

## 三步接线

### 1. 拿到 MCP 二进制

发布资产里有 `repotory-mcp-<平台>`；从源码构建则是：

```bash
go build -o /usr/local/bin/repotory-mcp ./cmd/mcp
```

### 2. 起一个本地 OpenAI 兼容端点

ollama（默认 `http://127.0.0.1:11434`）：

```bash
ollama serve
ollama pull qwen2.5-coder:7b     # 需要支持 tool calling 的模型
```

LM Studio：在应用里加载模型，然后开本地服务器（默认 `http://127.0.0.1:1234`，也可 `lms server start`）。

模型的工具调用能力是硬要求。3B 级别能完成单步工具调用，但一题多步就会跑偏；**配方能用小模型演示，真实使用建议 7B 以上**。

### 3. 把 MCP 注册进客户端

Claude Code：

```bash
claude mcp add repotory -- /usr/local/bin/repotory-mcp
```

Cursor / VS Code / Claude Code 的项目级配置文件都是同一个形状：

```json
{
  "mcpServers": {
    "repotory": { "command": "/usr/local/bin/repotory-mcp", "args": [] }
  }
}
```

opencode：

```bash
opencode mcp add repotory -- /usr/local/bin/repotory-mcp
```

opencode 的服务器名会成为工具前缀，所以你看到的工具名是 `repotory_repotory_context`、`repotory_repotory_handoff` 这种形式。想短一点就把服务器注册成别的名字（例如 `memory`，工具名变成 `memory_repotory_context`）。

**任何**能启动 stdio MCP server 的客户端都可以用同一行命令；协议只有 JSON-RPC，没有私有扩展。

## 两个调用把会话串起来

装好之后只需要两个动作，其余都是普通读写：

1. **开局**：`repotory_context`（可带 `path` 指向当前工作目录）。一次调用拿到技术栈、README 摘录、最近提交、待办、相关笔记——**最新的交接笔记完整置顶**。
2. **收尾**：`repotory_handoff`（`summary` 必填，另有 `changes` / `decisions` / `gotchas` / `next_steps`）。落库时带 `handoff` 标签，之后的 `notes_update` 拒绝覆盖它。

给客户的提示词可以就直接写这两句：

```text
开始工作前调用 repotory_context（path = 当前仓库目录）并读完返回的上下文。
结束工作前调用 repotory_handoff，写清这次做了什么、踩了什么坑、下一步该做什么。
```

不依赖模型自觉的做法有两条，且都已经是产品能力（[ADR-0043](../adr/0043-handoff-instructions-in-agent-instruction-files.md)）：

- **Claude Code 的 SessionEnd hook**（见 [AI 集成](ai-integration.md) 的「会话结束自动交接」一节）——会话一结束必定触发，**只有这一家客户端有**。
- **把上面那两句写进项目的 `AGENTS.md` 与 `CLAUDE.md`**——`repotory-init --with-instructions` 或桌面端「接入 AI 客户端」里那个勾选就是干这件事的，任何读这两个文件的 agent（opencode / Codex / Cursor…）都会照做。

两条并存：hook 兜住「忘了收尾」，指令文件兜住「根本不用 Claude Code」。指令文件把「记一笔」从必定发生变成大概率发生——模型读到了，服从不保证。

## 真机验证记录（2026-10-29）

环境：macOS arm64；ollama `qwen2.5:3b`（1.9 GB，本地）；`repotory-mcp` v2.19.0；**隔离 HOME**（`HOME=/tmp/repotory-verify/home`），一个演示 Git 仓库。活库全程未打开，前后 sha256 一致。

| 步骤 | 结果 |
|------|------|
| `repotory_scan`（冷启动） | 1 仓库 / 1 项目，`success: true` |
| **会话 1**（新进程） | 18 个 MCP 工具进入模型的 tool schema → 模型发出 `repotory_handoff` → 落库 `note_id: 1` |
| **会话 2**（另一个新进程，不共享对话历史） | 模型发出 `repotory_context` → 返回的上下文**以会话 1 那条交接笔记开头** → 模型一字不差地复述出会话 1 记的 gotcha |
| 抓包（ollama 前面挂的 loopback 记录代理） | `POST /v1/chat/completions`，`tools: 18`，目标 `127.0.0.1` |

会话 2 的模型回答（原样）：

```text
The gotcha recorded in the previous session was that the `/health` endpoint
returns a `200` status code even when the account balance is negative.
```

**这条链路里没有任何一步需要联网**：模型在本地、库在本地、两者之间是 stdio 管道。

## 已知限制：opencode 2.0.29 不把 MCP 工具交给模型

这次验证最先试的客户端是 opencode v2.0.29，结果是它**连上了 MCP 却不用**：

- 日志明确写着 `mcp connected server=repotory tools=18`；
- 但发给模型的请求里只有 opencode 自带的 12 个工具，**18 个 repotory 工具一个都没有**；
- 换模型（ollama qwen2.5:3b / qwen2.5-coder:7b、LM Studio 27B）、换配置形状（`mcp.servers` / `mcp` / 项目级 / 全局级）、换 MCP server（另写一个只有 1 个工具的对照 server）——**全都不行**，对照 server 的工具同样进不去。

所以这是客户端侧的行为，与模型、与 `repotory-mcp` 都无关。opencode 自己的文档对「工具调用不工作」只给了一条线索（把 ollama 的 `num_ctx` 调到 16k–32k），本文的复现里调了上下文也不生效。

在 opencode 修好之前，用 Claude Code / Cursor / 任何别的 MCP 客户端接入 Repotory；或者按下面「记录用的最小驱动」自己验证一遍链路。

## 记录用的最小驱动

上表里的两个会话是用一个约 170 行的记录脚本跑的（`outputs/process-logs/2026-10-29-本地Agent-MCP真机验证.md` 附全文）：它做三件事——通过 stdio 连 `repotory-mcp`、把 18 个工具原样转成 OpenAI 的 `tools` 数组、把模型的 `tool_calls` 转成 MCP 调用。

**它不是要发布的产品，只是把「客户端本该做的事」显式写出来的记录工具**。真实使用请用现成的客户端；这个脚本的用途是：当你不确定是「客户端没接上」还是「服务端有问题」时，它能用三十行代码把链路跑通，从而把问题定位到客户端那一侧。

## 边界

- **Repotory 不提供模型**，也不替你选模型。离线是两件事相加：模型在你机器上 + 库在你机器上。
- **不保证模型会主动调工具**。小模型需要把指令写死（「先调 repotory_context」）；把它写进客户端提示词或 hook 才是可靠做法。
- **这是单用户本地库**。跨设备、多人协作不在范围内（见[数据与备份](../data-management.md)）。
- 语义检索（可选）会打 embedding 端点——**如果你要全离线，就用本地的 ollama embedding**（`go run ./cmd/vector-init` 的默认推荐就是本地 Ollama），别填远端地址。
