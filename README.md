# M365Plus

基于 Go 实现的代理服务，将 Microsoft 365 Copilot 的 WebSocket（SignalR）接口转换为 OpenAI / Anthropic 兼容的 HTTP API，并内置多账号管理的 Web 管理面板。

本项目 fork 自 [M365Bridge](https://github.com/KilimcininKorOglu/M365Bridge)。

## 架构

```
你的应用 ──▶ M365Plus (OpenAI/Anthropic API + Web 管理面板) ──▶ substrate.office.com (SignalR) ──▶ M365 Copilot 后端
```

## 功能特性

- **Web 管理面板**（`web/`）：账号池管理、API Key 管理、模型测试、聊天控制台、用量日志、会话管理
- **多账号支持**：支持添加、切换多个 M365 Copilot 账号，可选自动轮换（`M365_AUTO_ROTATE_ACCOUNTS`）
- **文本对话**：支持流式与非流式输出
- **多模态图片输入**（OpenAI `image_url` 与 Anthropic `image` 块；支持 data URL、http(s) URL 与本地文件路径）
- **图片生成**（`/v1/images/generations`、`/v1/images/edits`），支持 `url` 与 `b64_json` 两种响应格式
- **多轮对话**：基于 ConversationId 追踪与按会话隔离
- **思考 / 推理内容提取**（OpenAI 的 `reasoning_content`，Anthropic 的 `thinking` 块）
- **模拟工具调用**：OpenAI 与 Anthropic 端点均支持（流式与非流式），兼容 Codex 的 `custom` 自由格式工具
- **OpenAI 兼容端点**（Chat Completions、Responses、Compact、Images）
- **Anthropic 兼容端点**（专用 SSE 处理器）
- **API Key 认证**（`M365_API_KEYS` / `M365_API_KEY`）
- **max_tokens 强制执行**（所有端点均生效，tiktoken BPE）
- **可选的本地编码工具**（文件 / Git / Shell），由 `M365_ENABLE_CODE_TOOLS` 控制
- **CLI 接口**：交互模式与单次查询模式

## 环境要求

- **Go 1.22+**（[下载](https://go.dev/dl/)）
- **git**，用于克隆本仓库
- 具备 **Microsoft 365 Copilot 许可**（商业或企业账号，且开通 Copilot 权限）
- 一个已登录 [https://m365.cloud.microsoft](https://m365.cloud.microsoft) 的浏览器，用于提取 token（setup wizard）

## 安装

```bash
git clone https://github.com/ryc2077/m365plus
cd m365plus
go mod download
go build -o bin/m365-bridge ./cmd/cli
```

### Docker

```yaml
services:
  m365bridge:
    build: .
    container_name: m365bridge
    ports:
      - "8230:8000"
    volumes:
      - ./data:/app/data
    restart: unless-stopped
```

```bash
docker compose up -d
```

API 地址为 `http://localhost:8230`。

## 配置向导

### 第一步：从浏览器提取 token

打开 [https://m365.cloud.microsoft](https://m365.cloud.microsoft) 并登录，按 **F12** 打开开发者工具 → **Console**，运行 `setup-wizard` 命令打印的提取脚本。脚本会输出包含 `oid`、`tenant`、`refresh_token`（若浏览器支持 `cookieStore` API，还会包含 `sso_cookies`）的 JSON。

将其保存到 `data/setup.json`：

```json
{
  "oid": "your-oid",
  "tenant": "your-tenant",
  "refresh_token": "your-refresh-token",
  "sso_cookies": [
    {"name": "ESTSAUTH", "value": "..."},
    {"name": "ESTSAUTHPERSISTENT", "value": "..."}
  ]
}
```

### 第二步：运行配置向导

```bash
./bin/m365-bridge setup-wizard --file data/setup.json
```

向导将完成：
- 将 `oid` / `tenant` / `refresh_token` 写入 `data/.env`
- 使用 AES-256-GCM 将 SSO cookies（如提供）加密保存到 `data/tokens/`
- 通过换取访问令牌验证 token 是否有效

> **注意：** 微软 SPA refresh token 大约 24 小时后过期。配置了 `sso_cookies` 时服务端会自动续期；否则过期后需要重新运行向导。

### 第三步：启动服务

```bash
./bin/m365-bridge serve --port 8000
```

浏览器访问 `http://localhost:8000` 打开 Web 管理面板（首次运行密码通过 `M365_ADMIN_PASSWORD`、`M365_ADMIN_PASSWORD_FILE` 或 `M365_ADMIN_PASSWORD_BOOTSTRAP_FILE` 配置/引导生成）。可在此添加更多账号并管理账号池。

## 使用说明

### CLI

```bash
# 单次查询
./bin/m365-bridge "your question"

# 交互模式
./bin/m365-bridge -i

# 使用推理模型
./bin/m365-bridge --model gpt5.5-reasoning "your question"

# 列出模型
./bin/m365-bridge --list-models
```

### 子命令

| 命令 | 说明 |
|------|------|
| `serve` | 启动 HTTP API 服务（`--port`，默认 `8000`） |
| `setup-wizard` | 浏览器配置向导（`--file`，默认 `data/setup.json`） |

### API 服务

```bash
# 在 8000 端口启动 API 服务
./bin/m365-bridge serve --port 8000

# 使用 curl 测试
curl http://127.0.0.1:8000/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer your-api-key" \
  -d '{"model":"gpt5.5","messages":[{"role":"user","content":"Hello"}]}'
```

## API 端点

| 端点                              | 说明                                              |
|-----------------------------------|---------------------------------------------------|
| `POST /v1/chat/completions`       | OpenAI Chat Completions（流式 + 非流式）          |
| `POST /v1/completions`            | OpenAI 文本补全（流式 + 非流式）                  |
| `POST /v1/responses`              | OpenAI Responses API（流式 + 非流式）             |
| `POST /v1/responses/compact`      | OpenAI Responses Compact API（Codex 远程压缩）    |
| `POST /v1/messages`               | Anthropic Messages 格式（专用 SSE 处理器）        |
| `POST /v1/messages/count_tokens`  | Anthropic 输入 token 计数                         |
| `POST /v1/complete`               | Anthropic Complete（FIM）                         |
| `POST /v1/images/generations`     | OpenAI Images API：从文本生成图片（JSON 请求体）  |
| `POST /v1/images/edits`           | OpenAI Images API：编辑现有图片（multipart）      |
| `GET /v1/models`                  | 模型列表                                          |
| `GET /health`                     | 健康检查（无需认证）                              |

### Web 管理 API

`serve` 启动后挂载于 `/api/` 路径下（需通过管理面板认证）：

| 端点 | 说明 |
|------|------|
| `POST /api/admin/login` / `logout` | 管理员会话管理 |
| `POST /api/admin/change-password` | 轮换管理员密码 |
| `GET/PUT /api/admin/keys` | API Key 存储 |
| `GET /api/admin/models`, `POST /api/admin/models/test` | 模型注册表 + 往返测试 |
| `POST /api/admin/chat` | 管理面板聊天 |
| `GET/PUT /api/admin/settings` | 运行时设置 |
| `GET /api/admin/proxy-pool` | 出站代理池状态 |
| `GET/PUT /api/admin/deployments`, `POST /api/admin/deployments/...` | 部署管理 |
| `GET /api/accounts`, `POST /api/accounts/refresh` / `delete` / `switch` | 账号池管理 |
| `GET/POST /api/auth/start` / `status` / `callback` | PKCE 账号登录流程 |
| `GET/DELETE /api/conversations*` | 会话管理与清理 |
| `GET /api/health`, `GET /api/version` | 健康与版本信息 |

## 模型

模型选择通过发送给 M365 后端的 `tone` 字段实现。

| 键                          | Tone               | OpenAI ID          | 思考？ |
|-----------------------------|--------------------|--------------------|--------|
| `auto`                      | Magic              | gpt-4-auto         | 否     |
| `quick`                     | Chat               | gpt-4-quick        | 否     |
| `reasoning`                 | Magic              | gpt-4-reasoning    | 否     |
| `gpt5.5`                    | Gpt_5_5_Chat       | gpt-5.5            | 否     |
| `gpt5.5-reasoning`          | Gpt_5_5_Reasoning  | gpt-5.5-reasoning  | 是     |
| `gpt5.6-reasoning`          | Gpt_5_6_Reasoning  | gpt-5.6-reasoning  | 是     |
| `claude-sonnet`             | Claude_Sonnet      | claude-sonnet-4.6  | 否     |
| `claude-opus`               | Claude_Opus        | claude-opus-4.6    | 否     |

可以在模型名中用 `:` 分隔符内嵌会话 ID，例如 `gpt5.5-reasoning:my-session-001`，等同于发送 `X-Session-Id` 头。

## 图片生成

### `/v1/images/generations`（JSON 请求体）

```bash
curl http://127.0.0.1:8000/v1/images/generations \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer your-api-key" \
  -d '{"prompt":"a cute robot holding a red balloon","size":"1024x1024","response_format":"url"}'
```

参数：`prompt`（必填）、`n`、`size`、`quality`、`style`、`response_format`、`session_id`。

响应格式：

| 格式 | 行为 |
|------|------|
| `url`（默认） | 直接透传上游 `designerapp.officeapps.live.com` 的图片 URL（与 M365-Copilot2API 行为一致；无需 SSO cookies） |
| `b64_json` | 服务端下载图片（需通过 SSO cookies 获取 designer broker token）并返回 base64 编码的 PNG 数据 |

### `/v1/images/edits`（multipart/form-data）

使用文本提示编辑现有图片；支持通过重复的 `image` 表单字段提交最多 16 张图片，另含 `prompt`、`n`、`size`、`response_format`。

## 图片输入

所有对话端点均支持多模态图片输入：

- **OpenAI**：`content` 数组中的 `{"type": "image_url", "image_url": {"url": "..."}}` 块——URL 可以是 `data:` URL、http(s) URL 或本地文件路径
- **Anthropic**：`content` 数组中的 `{"type": "image", "source": {...}}` 块
- **Responses API**：带 `image_url` 的 `input_image` 内容块

图片通过 `POST https://substrate.office.com/m365Copilot/UploadFile` 上传至 M365，并作为附件附加到 WebSocket 消息中。支持格式：PNG、JPEG、GIF、WebP。

## 工具调用

M365Plus 支持**模拟工具调用**——客户端定义的工具无需 M365 原生支持即可工作。完整的请求 JSON 会被嵌入发送给 M365 Copilot 的提示词中；返回的 JSON 载荷被解析为 OpenAI `tool_calls` 或 Anthropic `tool_use` 块。

- 支持 OpenAI（`/v1/chat/completions`、`/v1/responses`）与 Anthropic（`/v1/messages`）端点，流式与非流式均可
- 支持 Codex 风格的 `custom` 自由格式工具（参数为原始 JS 源码文本，嵌套工具结果应通过 `text(...)` 辅助函数返回）
- 缺少 schema 必填参数的工具调用会被丢弃，并触发一次纠正性重问
- `tool_result` / `tool_use` 历史块在发送给 M365 前会被扁平化为纯文本
- 流式端点会先缓冲完整响应再解析工具调用（JSON 可能跨多个 chunk）

## 内置编码工具（可选）

当 `M365_ENABLE_CODE_TOOLS=1` 时，对话端点可执行受限的本地工具集。

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `M365_ENABLE_CODE_TOOLS` | `0` | 本地工具执行的总开关 |
| `M365_AUTO_EXPOSE_TOOLS` | `0` | 客户端未提供工具时自动注入内置工具 schema |
| `M365_WORKSPACE_DIR` | `.` | 限制文件与 Git 操作的范围 |
| `M365_CODE_TOOL_TIMEOUT` | `30s` | 每条命令/测试的超时时间（Go duration） |
| `M365_CODE_TOOL_MAX_OUTPUT` | `1048576` | 捕获的命令输出的最大字节数 |
| `M365_CODE_TOOL_MAX_READ_BYTES` | `1048576` | 文件读取返回的最大字节数 |
| `M365_CODE_TOOL_MAX_ITERATIONS` | `10` | 每次请求的最大模型/工具循环次数 |

**安全提示：** 启用这些工具将使 API 成为远程代码与文件访问入口。启用前务必配置 `M365_API_KEYS` / `M365_API_KEY`，使用专用工作目录，且未经严格隔离不要将此类部署暴露到公网。

## 配置项

配置通过环境变量读取（可写入 `data/.env` 或 shell 环境）。主要变量：

| 变量 | 说明 |
|------|------|
| `M365_TENANT_ID`, `M365_USER_OID` | 租户与用户对象 ID（CLI 模式必需） |
| `M365_CLIENT_ID`, `M365_SCOPE`, `M365_REDIRECT_URI` | OAuth 应用配置 |
| `M365_API_KEYS`, `M365_API_KEY` | 逗号分隔的 API Key / 单个 Key（`/v1/*` 认证） |
| `M365_ADMIN_PASSWORD`, `M365_ADMIN_PASSWORD_FILE`, `M365_ADMIN_PASSWORD_BOOTSTRAP_FILE` | 管理面板凭据 |
| `M365_AUTO_ROTATE_ACCOUNTS` | 启用账号自动轮换 |
| `M365_MAX_REQUESTS_PER_ACCOUNT` | 每个账号的轮换请求上限 |
| `M365_CONTEXT_WINDOW`, `M365_MAX_OUTPUT_TOKENS` | `/v1/models` 中声明的上下文/输出 token 提示 |
| `M365_INCREMENTAL_CONTEXT` | 增量上下文模式 |
| `M365_PROXY_POOL` | 逗号分隔的出站代理列表（`http://user:pass@host:port`） |
| `M365_CHAT_TIMEOUT_SECONDS`, `M365_IMAGE_TIMEOUT_SECONDS` | 上游超时 |
| `M365_LOG_LEVEL` | 日志级别（默认 `info`） |
| `M365_DATA_DIR`, `M365_CONFIG`, `M365_SESSION_CACHE`, `M365_TOKEN_CACHE` | 数据 / 配置 / 缓存路径 |
| `M365_OUTBOUND_PROXY` | 所有上游请求的单一出站代理 |

## 会话隔离

每个会话对应一个独立的 M365 会话。会话 ID 按以下优先级解析：

1. 请求体中的 `session_id` 字段
2. 请求体中的 `user` 字段
3. `X-Session-Id` 请求头
4. `hash(api_key + 第一条用户消息)`（无认证时为 `hash(第一条用户消息)`）

哈希兜底策略让无法发送自定义请求头的标准 OpenAI/Anthropic 客户端也能根据提示词自动获得独立的会话隔离。

## 项目结构

```
cmd/cli/main.go          # 入口：serve / setup-wizard / CLI
pkg/
  auth/                  # TokenManager、token 刷新、SSO cookie 重认证、designer token
  accounts/              # 账号池存储、轮换、CDP 刷新
  client/                # M365Client、WebSocket（SignalR）通信
  crypto/                # AES-256-GCM 加密
  models/                # 版本、模型注册表、配置
  payload/               # 请求载荷构建
  servers/               # HTTP API 服务、全部端点、CLI 服务
  setup/                 # 配置向导
  toolcalling/           # 模拟工具调用解析与提示词构建
  webadmin/              # 管理面板：账号、Key、设置、用量、聊天
web/                     # 管理面板前端（HTML/JS）
data/                    # 运行时数据（gitignored）：tokens/、setup.json、cache/
```

## 安全

- refresh token 与 SSO cookies 在存储前使用 AES-256-GCM 加密
- 加密密钥保存在 `data/tokens/encryption.key`；丢失该密钥将导致加密凭据不可读
- 访问令牌缓存短过期时间，并在后台主动刷新
- 配置后，API Key 认证保护所有 `/v1/*` 端点
- 代码与仓库中不保存任何凭据；`data/` 已被 gitignore

## 免责声明

本项目仅供学习与研究使用，用于探索公开可观察的网络通信协议。

使用本项目即表示您确认：
- 拥有合法的 Microsoft 365 Copilot 授权
- 仅用于个人学习与研究，而非商业用途
- 了解使用非官方接口的风险
- 接受由此产生的全部后果

本项目不会破解加密或绕过认证、不会访问或泄露他人数据、不会干扰微软服务。本项目与微软公司无任何关联。

## 许可证

Research Only
# Non-Docker LXC installation

Debian and Ubuntu LXC containers can run M365Plus directly with systemd. The release package includes M365Plus, the web interface, and sing-box for both amd64 and arm64.

```bash
curl -fsSL https://raw.githubusercontent.com/ryc2077/m365plus/master/scripts/install-lxc.sh | sudo sh
```

The service listens on port `8234`. Persistent data is stored in `/var/lib/m365plus`, configuration in `/etc/m365plus/m365plus.env`, and logs are available through `journalctl -u m365plus -f`. Run the same installer again to upgrade while preserving data.
