# SeekWell

SeekWell 是一个 DeepSeek 专用的本地模型网关。当前开发版提供渠道配置、OpenAI 兼容模型转发，以及 DeepSeek 官方 Search / Fetch 的转发入口。项目仍在首版开发中，OpenCode Go 的真实调用已完成初步验证，其余渠道和完整产品验收尚待验证。

## 已实现

- 在管理界面添加、编辑、启停和删除 DeepSeek 官方、OpenCode Go 或兼容 API 渠道；每个配置对应一个账号及一组模型映射。可以查询上游当前的 DeepSeek 模型目录，也可以按渠道发起一次最小真实补全测速，查看总耗时与上游状态码；付费渠道测速前需确认费用。可按用户确认的工具调用、图片、Responses 和 Anthropic Messages 能力筛选渠道。
- 将同名统一 DeepSeek 模型按渠道优先级转发到上游；上游认证失败、限流、服务错误或连接失败时，在开始向客户端输出前尝试下一个渠道。
- 转发 OpenAI Chat Completions 与 Responses；流式响应原样转发。上游是否支持对应协议及功能由用户确认。支持通过 `X-OpenCode-Session`、`X-SeekWell-Session-Id`、`X-Session-ID` 或 DSH 的 `X-DeepSeek-Harness-Session-Id` 保持进程内会话亲和；绑定一小时后过期，重启不保留。
- 对支持该协议并已配置 Messages 地址的渠道原样转发 Anthropic Messages，不做跨协议转换。
- 转发 DeepSeek 官方 Search / Fetch，并可在管理界面测试搜索。搜索使用渠道类型为「DeepSeek 官方」且允许自动调用的账号。
- 付费渠道默认不参与自动调用；在渠道配置中明确启用后才参与。管理界面展示请求尝试的渠道和状态，不保存请求正文。

## 启动

需要 Go 1.27.4 和 Node.js 26.13.1（或更新的兼容稳定版本）。Go 默认允许通过 `GOTOOLCHAIN=auto` 获取项目指定工具链。

```bash
cd apps/web
npm ci
npm run build
cd ../server
go run ./cmd/seekwell
```

访问 <http://127.0.0.1:8350>。服务默认只监听本机；`SEEKWELL_DATA` 可指定数据目录，`SEEKWELL_WEB` 可指定前端构建目录。默认数据目录为 `apps/server/data`；SQLite 数据库包括渠道凭据，请按敏感配置保护和备份。

在界面添加渠道，填写包含 `/v1` 的 API 根地址、API Key、统一模型名和上游模型名。OpenCode Go 可直接选择对应渠道类型，详见 [渠道说明](docs/channels/opencode-go.md)。名称都应包含 `deepseek`。根据真实上游确认并勾选工具调用、图片、Responses 和 Messages 能力，未勾选时对应请求不会被路由到该渠道。同一个统一模型可以配置多个渠道，数字较小的优先。官方搜索和付费模型如需自动调用，要勾选「允许自动使用此付费渠道」。

客户端示例（请将模型名替换为已配置的统一模型）：

```bash
curl http://127.0.0.1:8350/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{"model":"deepseek-flash","messages":[{"role":"user","content":"你好"}]}'
```

搜索示例：

```bash
curl http://127.0.0.1:8350/v1/tools/search \
  -H 'Content-Type: application/json' \
  -d '{"query":"DeepSeek","count":5}'
```

## 当前限制

- WorkBuddy 等产品账号的直接登录，以及 OpenCode 的账号登录和额度查询尚未实现。OpenCode Go 支持直接使用 API Key。若已有兼容 API 网关，可以作为兼容渠道填写，但具体模型和协议需要实测。
- 尚不支持客户端搜索工具的自动转换、模型能力自动发现或跨渠道文件 / 会话转换。模型目录查询仅展示上游 ID，不会自动验证或建立映射。Responses 和 Messages 只是向支持该协议的上游接口透传，不是跨协议转换；原生文件接口还未实现。
- 渠道能力、真实版本和上下文不会自动验证；能力开关由用户根据渠道实际情况确认，配置同名模型也意味着用户自行确认版本兼容。未实现限流冷却、额度优先或按费用计量。会话亲和只基于请求头和进程内状态，不负责转换上游专有会话或文件 ID。
- 已开始输出的流中断不会自动拼接其他渠道的输出；尝试记录目前只包含模型、渠道、状态和耗时，不包含搜索记录或 token 费用。
- API 和管理界面无访问认证，只应在受信任的本机环境运行；服务未设计为直接暴露到公网。凭据存储在本地 SQLite 中，未加密。
- OpenCode Go 的真实 API 验证见渠道说明；尚无 DeepSeek 官方 API、OpenDesign、WorkBuddy 或真实 DSH 客户端的端到端验收。模拟上游通过不等于这些渠道验收通过。

整体方案与仍待确认的决策见 [设计文档](docs/design.md)。
