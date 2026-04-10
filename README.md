# Go Banana Proxy

一个基于 Go 标准库实现的多上游图片生成代理服务。

它的目标很明确：

- 通过 `UpstreamProvider` 接口对接多个上游图片生成 API
- 对外提供 Gemini 风格接口（主）和 OpenAI 风格接口（备选）
- 统一处理上游鉴权、错误映射、代理 URL 替换与安全白名单

整个项目为单模块、单二进制、零第三方依赖。

## 1. 功能概览

- Gemini 图片生成（主接口）：`POST /v1beta/models/{model}:generateContent`
- OpenAI 同步图片生成（备选）：`POST /v1/images/generations`
- 健康检查：`GET /health`

## 2. 支持的上游

| 上游 | 域名 | 模式 | 文件 |
|------|------|------|------|
| grsai | `api.grsai.com` | 同步单次请求 | `upstream_grsai.go` |
| aiapidev | `www.aiapidev.com` | 异步创建+轮询 | `upstream_aiapidev.go` |

### grsai 模型映射

| Gemini 模型名 | 上游模型名 |
|---------------|-----------|
| `gemini-3-pro-image-preview` | `nano-banana-pro` |
| `gemini-2.5-flash-image` | `nano-banana-fast` |
| `gemini-3.1-flash-image-preview` | `nano-banana-2` |

### aiapidev 模型映射

| Gemini 模型名 | 上游模型名 |
|---------------|-----------|
| `gemini-3-pro-image-preview` | `nanobananapro` |
| `gemini-3.1-flash-image-preview` | `nanobanana2` |

aiapidev 仅支持 Gemini 接口，不支持 OpenAI 接口。
请求体自动从 Gemini camelCase 改写为 aiapidev snake_case + file_data 格式。
轮询间隔：10s → 30s → 每 10s，超时 600s。

## 3. 核心设计

- 纯标准库实现，没有 HTTP 框架依赖
- `UpstreamProvider` 接口抽象上游差异（路径、字段映射、模型名、响应解析）
- `UpstreamExecutor` 可选接口，供异步轮询类上游（如 aiapidev）控制完整请求生命周期
- `providerRegistry` 按请求中的 base-url 自动路由到对应 Provider
- 图片代理由外部服务提供，本服务仅拼接代理 URL 前缀

## 4. 运行方式

### 本地运行

```bash
PORT=8787 \
BANANA_BASE_URL=https://api.grsai.com \
PUBLIC_BASE_URL=https://your-proxy.example.com \
go run .
```

### 编译

```bash
go build -o banana-proxy .
```

### 测试

```bash
go test ./...
```

### Docker

```bash
docker build -t go-banana-proxy .

docker run --rm -p 8787:8787 \
  -e BANANA_BASE_URL=https://api.grsai.com \
  -e PUBLIC_BASE_URL=https://your-proxy.example.com \
  go-banana-proxy
```

## 5. 环境变量

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `PORT` | `8787` | 服务监听端口 |
| `BANANA_BASE_URL` | `https://api.grsai.com` | 默认上游地址，用于 auth fallback |
| `PUBLIC_BASE_URL` | 空（回退为请求 origin） | 外部代理服务的 URL 前缀 |

## 6. 上游鉴权

支持以下几种写法：

- `Authorization: Bearer <api-key>`
- `Authorization: Bearer <base-url>|<api-key>`
- `X-Upstream-Base-Url: <base-url>` + `Authorization: Bearer <api-key>`
- Gemini 兼容请求允许用 `x-goog-api-key`

根据解析出的 base-url 自动路由到对应 Provider。
未匹配的 base-url 回退到 grsai（默认 Provider）。

## 7. 返回值约定

- Gemini 风格接口返回 `candidates[].content.parts`（含 `finishReason`、`safetyRatings`、`usageMetadata`）
- OpenAI 风格接口返回代理后的 `data[].url` + `upstream_meta`
- 所有错误都会保留上游详情（HTTP 状态、错误码、错误消息、原始响应片段）

## 8. 目录说明

| 文件 | 职责 |
|------|------|
| `main.go` | 入口和环境变量加载 |
| `server.go` | 路由分发、Server 初始化、Provider 注册 |
| `auth.go` | 上游鉴权解析 |
| `upstream.go` | `UpstreamProvider`/`UpstreamExecutor` 接口、类型定义、注册表 |
| `upstream_grsai.go` | grsai 同步 Provider 实现 |
| `upstream_aiapidev.go` | aiapidev 异步轮询 Provider 实现 |
| `sync.go` | 图片生成核心逻辑、Gemini/OpenAI 双格式输出 |
| `errors.go` | 错误响应格式（Gemini/OpenAI） |
| `helpers.go` | JSON 辅助、代理 URL 拼接 |
| `utils.go` | 编解码、类型转换、时间处理等工具函数 |

## 9. 扩展新上游

1. 新建 `upstream_xxx.go`，实现 `UpstreamProvider` 接口（同步上游）或同时实现 `UpstreamExecutor`（异步上游）
2. 在 `server.go` 的 `NewServer` 中注册该 Provider
3. 无需修改 `sync.go`、`auth.go` 等现有代码
