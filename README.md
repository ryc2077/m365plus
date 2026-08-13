# M365Plus

A Go implementation that converts Microsoft 365 Copilot's WebSocket (SignalR) interface into OpenAI / Anthropic compatible HTTP APIs, with a web admin console for multi-account management.

Forked from [M365Bridge](https://github.com/KilimcininKorOglu/M365Bridge).

## Architecture

```
Your App ──▶ M365Plus (OpenAI/Anthropic API + Web Admin) ──▶ substrate.office.com (SignalR) ──▶ M365 Copilot Backend
```

## Features

- **Web admin console** (`web/`): account pool, API-key store, model testing, chat console, usage logs, conversation management
- **Multi-account support**: add and switch between multiple M365 Copilot accounts, optional automatic rotation (`M365_AUTO_ROTATE_ACCOUNTS`)
- **Text chat** with streaming / non-streaming output
- **Multimodal image input** (OpenAI `image_url` and Anthropic `image` blocks; data URLs, http(s) URLs and local file paths are all supported)
- **Image generation** (`/v1/images/generations`, `/v1/images/edits`) with `url` and `b64_json` response formats
- **Multi-turn conversation** via ConversationId tracking and per-session isolation
- **Thinking / reasoning extraction** (`reasoning_content` for OpenAI, `thinking` blocks for Anthropic)
- **Simulated tool calling** for both OpenAI and Anthropic endpoints (streaming and non-streaming), including Codex-compatible `custom` freeform tools
- **OpenAI-compatible endpoints** (Chat Completions, Responses, Compact, Images)
- **Anthropic-compatible endpoints** (dedicated SSE handlers)
- **API key authentication** (`M365_API_KEYS` / `M365_API_KEY`)
- **max_tokens enforcement** across all endpoints (tiktoken BPE)
- **Optional local coding tools** (file/git/shell) gated by `M365_ENABLE_CODE_TOOLS`
- **CLI interface** for interactive / single-query use

## Prerequisites

- **Go 1.22+** installed ([download](https://go.dev/dl/))
- **git** for cloning this repository
- A **Microsoft 365 Copilot license** (business or enterprise account with Copilot access)
- A browser logged into [https://m365.cloud.microsoft](https://m365.cloud.microsoft) for token extraction (setup wizard)

## Installation

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

The API is available at `http://localhost:8230`.

## Setup

### Step 1: Get tokens from your browser

Open [https://m365.cloud.microsoft](https://m365.cloud.microsoft), log in, press **F12** → **Console**, and run the extraction snippet from `pkg/setup/wizard.go` (or the `setup-wizard` command prints it). It outputs JSON with `oid`, `tenant`, and `refresh_token` (plus `sso_cookies` when the `cookieStore` API is available).

Save it to `data/setup.json`:

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

### Step 2: Run the setup wizard

```bash
./bin/m365-bridge setup-wizard --file data/setup.json
```

The wizard:
- Saves `oid` / `tenant` / `refresh_token` to `data/.env`
- Encrypts SSO cookies (if provided) with AES-256-GCM into `data/tokens/`
- Verifies the token by exchanging it for an access token

> **Note:** Microsoft SPA refresh tokens expire after ~24 hours. With `sso_cookies`, the server renews automatically. Without them, re-run the wizard when tokens expire.

### Step 3: Start the server

```bash
./bin/m365-bridge serve --port 8000
```

Open `http://localhost:8000` for the web admin console (first-run password is generated/bootstrapped via `M365_ADMIN_PASSWORD`, `M365_ADMIN_PASSWORD_FILE`, or `M365_ADMIN_PASSWORD_BOOTSTRAP_FILE`). You can add additional accounts and manage the pool there.

## Usage

### CLI

```bash
# Single query
./bin/m365-bridge "your question"

# Interactive mode
./bin/m365-bridge -i

# Reasoning model
./bin/m365-bridge --model gpt5.5-reasoning "your question"

# List models
./bin/m365-bridge --list-models
```

### Subcommands

| Command | Description |
|---------|-------------|
| `serve` | Starts the HTTP API server (`--port`, default `8000`) |
| `setup-wizard` | Browser-based setup wizard (`--file`, default `data/setup.json`) |

### API Server

```bash
# Start API server on port 8000
./bin/m365-bridge serve --port 8000

# Test with curl
curl http://127.0.0.1:8000/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer your-api-key" \
  -d '{"model":"gpt5.5","messages":[{"role":"user","content":"Hello"}]}'
```

## API Endpoints

| Endpoint                         | Description                                            |
|----------------------------------|--------------------------------------------------------|
| `POST /v1/chat/completions`      | OpenAI Chat Completions (streaming + non-streaming)    |
| `POST /v1/completions`           | OpenAI text completion (streaming + non-streaming)     |
| `POST /v1/responses`             | OpenAI Responses API (streaming + non-streaming)       |
| `POST /v1/responses/compact`     | OpenAI Responses Compact API (Codex remote compaction) |
| `POST /v1/messages`              | Anthropic Messages format (dedicated SSE handlers)     |
| `POST /v1/messages/count_tokens` | Anthropic input token counting                         |
| `POST /v1/complete`              | Anthropic Complete (FIM)                               |
| `POST /v1/images/generations`    | OpenAI Images API: generate from text (JSON body)      |
| `POST /v1/images/edits`          | OpenAI Images API: edit existing image (multipart)     |
| `GET /v1/models`                 | Model list                                             |
| `GET /health`                    | Health check (no auth required)                        |

### Web Admin API

Mounted under `/api/` when `serve` starts (auth required via the admin console):

| Endpoint | Description |
|----------|-------------|
| `POST /api/admin/login` / `logout` | Admin session management |
| `POST /api/admin/change-password` | Rotate the admin password |
| `GET/PUT /api/admin/keys` | API-key store |
| `GET /api/admin/models`, `POST /api/admin/models/test` | Model registry + round-trip test |
| `POST /api/admin/chat` | Chat from the admin console |
| `GET/PUT /api/admin/settings` | Runtime settings |
| `GET /api/admin/proxy-pool` | Outbound proxy pool status |
| `GET/PUT /api/admin/deployments`, `POST /api/admin/deployments/...` | Deployment management |
| `GET /api/accounts`, `POST /api/accounts/refresh` / `delete` / `switch` | Account pool management |
| `GET/POST /api/auth/start` / `status` / `callback` | PKCE account login flow |
| `GET/DELETE /api/conversations*` | Conversation management and cleanup |
| `GET /api/health`, `GET /api/version` | Health and version |

## Models

Model selection is via the `tone` field sent to the M365 backend.

| Key                        | Tone              | OpenAI ID         | Thinking? |
|----------------------------|-------------------|-------------------|-----------|
| `auto`                     | Magic             | gpt-4-auto        | No        |
| `quick`                    | Chat              | gpt-4-quick       | No        |
| `reasoning`                | Magic             | gpt-4-reasoning   | No        |
| `gpt5.5`                   | Gpt_5_5_Chat      | gpt-5.5           | No        |
| `gpt5.5-reasoning`         | Gpt_5_5_Reasoning | gpt-5.5-reasoning | Yes       |
| `gpt5.6-reasoning`         | Gpt_5_6_Reasoning | gpt-5.6-reasoning | Yes       |
| `claude-sonnet`            | Claude_Sonnet     | claude-sonnet-4.6 | No        |
| `claude-opus`              | Claude_Opus       | claude-opus-4.6   | No        |

You can embed a session ID in the model name with a `:` separator, e.g. `gpt5.5-reasoning:my-session-001`, which is equivalent to sending `X-Session-Id`.

## Image Generation

### `/v1/images/generations` (JSON body)

```bash
curl http://127.0.0.1:8000/v1/images/generations \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer your-api-key" \
  -d '{"prompt":"a cute robot holding a red balloon","size":"1024x1024","response_format":"url"}'
```

Parameters: `prompt` (required), `n`, `size`, `quality`, `style`, `response_format`, `session_id`.

Response formats:

| Format | Behavior |
|--------|----------|
| `url` (default) | Passes the upstream `designerapp.officeapps.live.com` image URL through directly (matching M365-Copilot2API; no SSO cookies required) |
| `b64_json` | Downloads the image server-side (requires a designer broker token via SSO cookies) and returns base64-encoded PNG data |

### `/v1/images/edits` (multipart/form-data)

Edit existing image(s) with a text prompt; supports up to 16 images via repeated `image` form fields plus `prompt`, `n`, `size`, `response_format`.

## Image Input

Multimodal image input is supported on all chat endpoints:

- **OpenAI**: `content` array with `{"type": "image_url", "image_url": {"url": "..."}}` blocks — the URL may be a `data:` URL, an http(s) URL, or a local file path
- **Anthropic**: `content` array with `{"type": "image", "source": {...}}` blocks
- **Responses API**: `input_image` content parts with `image_url`

Images are uploaded to M365 via `POST https://substrate.office.com/m365Copilot/UploadFile` and attached to the WebSocket message. Supported formats: PNG, JPEG, GIF, WebP.

## Tool Calling

M365Plus supports **simulated tool calling** — client-defined tools work without M365 natively supporting them. The full request JSON is embedded into the prompt sent to M365 Copilot; the returned JSON payload is parsed into OpenAI `tool_calls` or Anthropic `tool_use` blocks.

- Works on OpenAI (`/v1/chat/completions`, `/v1/responses`) and Anthropic (`/v1/messages`) endpoints, streaming and non-streaming
- Codex-style `custom` freeform tools are supported (arguments are raw JS source text, and nested tool results should be surfaced via the `text(...)` helper)
- Tool calls missing schema-required arguments are dropped and a single corrective re-ask is issued
- `tool_result` / `tool_use` history blocks are flattened to plain text before being sent to M365
- Streaming buffers the full response before parsing tool calls (JSON may span chunks)

## Built-in Coding Tools (Opt-in)

Server-side local execution of a restricted toolset is available on chat endpoints when `M365_ENABLE_CODE_TOOLS=1`.

| Variable | Default | Description |
|----------|---------|-------------|
| `M365_ENABLE_CODE_TOOLS` | `0` | Main gate for local tool execution |
| `M365_AUTO_EXPOSE_TOOLS` | `0` | Inject built-in tool schemas when the client provides none |
| `M365_WORKSPACE_DIR` | `.` | Confines file and Git operations |
| `M365_CODE_TOOL_TIMEOUT` | `30s` | Per-command/test timeout (Go duration) |
| `M365_CODE_TOOL_MAX_OUTPUT` | `1048576` | Max captured command output bytes |
| `M365_CODE_TOOL_MAX_READ_BYTES` | `1048576` | Max bytes returned by a file read |
| `M365_CODE_TOOL_MAX_ITERATIONS` | `10` | Max model/tool loop iterations per request |

**Security:** this turns the API into a remote code and file access surface. Configure `M365_API_KEYS` / `M365_API_KEY` before enabling, use a dedicated workspace, and do not expose such a deployment publicly without strict isolation.

## Configuration

Configuration is read from environment variables (set in `data/.env` or the shell). Key variables:

| Variable | Description |
|----------|-------------|
| `M365_TENANT_ID`, `M365_USER_OID` | Tenant and user object ID (required for CLI mode) |
| `M365_CLIENT_ID`, `M365_SCOPE`, `M365_REDIRECT_URI` | OAuth app settings |
| `M365_API_KEYS`, `M365_API_KEY` | Comma-separated API keys / single key (auth on `/v1/*`) |
| `M365_ADMIN_PASSWORD`, `M365_ADMIN_PASSWORD_FILE`, `M365_ADMIN_PASSWORD_BOOTSTRAP_FILE` | Admin console credentials |
| `M365_AUTO_ROTATE_ACCOUNTS` | Enable automatic account rotation |
| `M365_MAX_REQUESTS_PER_ACCOUNT` | Rotation limit per account |
| `M365_CONTEXT_WINDOW`, `M365_MAX_OUTPUT_TOKENS` | Advertised context/output token hints in `/v1/models` |
| `M365_INCREMENTAL_CONTEXT` | Incremental context mode |
| `M365_PROXY_POOL` | Comma-separated outbound proxies (`http://user:pass@host:port`) |
| `M365_CHAT_TIMEOUT_SECONDS`, `M365_IMAGE_TIMEOUT_SECONDS` | Upstream timeouts |
| `M365_LOG_LEVEL` | Log level (default `info`) |
| `M365_DATA_DIR`, `M365_CONFIG`, `M365_SESSION_CACHE`, `M365_TOKEN_CACHE` | Data / config / cache paths |
| `M365_OUTBOUND_PROXY` | Single outbound proxy for all upstream requests |

## Session Isolation

Each session maps to a unique M365 conversation. The session ID is resolved in priority order:

1. `session_id` field in request body
2. `user` field in request body
3. `X-Session-Id` header
4. `hash(api_key + first_user_message)` (or `hash(first_user_message)` without auth)

The hash fallback gives standard OpenAI/Anthropic clients that cannot send custom headers automatic per-prompt conversation isolation.

## Project Structure

```
cmd/cli/main.go          # Entry point: serve / setup-wizard / CLI
pkg/
  auth/                  # TokenManager, token refresh, SSO cookie re-auth, designer token
  accounts/              # Account pool store, rotation, CDP refresh
  client/                # M365Client, WebSocket (SignalR) communication
  crypto/                # AES-256-GCM encryption
  models/                # Version, ModelRegistry, Config
  payload/               # Request payload builders
  servers/               # HTTP API server, all endpoints, CLI server
  setup/                 # Setup wizard
  toolcalling/           # Simulated tool-call parsing and prompting
  webadmin/              # Admin console: accounts, keys, settings, usage, chat
web/                     # Admin console frontend (HTML/JS)
data/                    # Runtime data (gitignored): tokens/, setup.json, cache/
```

## Security

- Refresh tokens and SSO cookies encrypted with AES-256-GCM before storage
- Encryption key stored in `data/tokens/encryption.key`; losing it makes encrypted credentials unreadable
- Access tokens cached with short expiry and proactively refreshed in the background
- API key authentication protects all `/v1/*` endpoints when configured
- No credentials stored in code or repository; `data/` is gitignored

## Disclaimer

This project is for learning and research purposes only. It explores publicly observable network communication protocols.

By using this project, you confirm that:
- You have legitimate Microsoft 365 Copilot authorization
- It is for personal learning and research, not commercial use
- You understand the risks of using unofficial interfaces
- You accept all consequences

This project does not crack encryption or bypass authentication, access or leak others' data, or interfere with Microsoft services. It has no association with Microsoft Corporation.

## License

Research Only
