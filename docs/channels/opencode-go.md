# OpenCode Go 渠道

SeekWell 可通过用户提供的 OpenCode Go API Key，调用该套餐当前提供的 DeepSeek 模型。渠道类型选择 `OpenCode Go`，API 根地址默认是 `https://opencode.ai/inference/go/v1`。统一模型名可与上游模型 ID 不同：例如使用 `deepseek-flash` 作为统一名，将上游模型配置为 `deepseek-v4.1-flash`。Pro 可以创建另一条渠道映射，使用 `deepseek-v4-pro`。

上游模型、区域、套餐资格、额度与条款可能改变，应以 [OpenCode 官方 Go 文档](https://opencode.ai/docs/console/go/)和实际账号为准。套餐额度可能超出后产生费用，是否自动调用该渠道由用户自己勾选。

根据官方要求，SeekWell 用自己的 `SeekWell/0.1` 标识请求，并向 OpenCode Go 发送 `x-opencode-session`。优先使用客户端传入的 OpenCode、SeekWell、`X-Session-ID` 或 DSH 会话头；客户端没有会话头时为**每次请求**创建一个随机标识，不将无关请求假装是同一会话。后者不能跨请求保持上游缓存。SeekWell 的进程内会话亲和在有客户端会话头时生效，绑定一小时，重启后失效。

## 用量查询

渠道列表加载后，SeekWell 会自动只读请求 `GET https://opencode.ai/zen/go/v1/usage`，展示上游返回的 5 小时、每周、每月窗口已用百分比与重置时间；缺失的窗口不会补成 0。仅当渠道 BaseURL 的 host 为 `opencode.ai` 且使用 HTTPS 时才发送 API Key；自定义兼容网关显示为不支持查询，SeekWell 不会把 Key 发往该地址。

该端点来自 CLI 的账号用量实现，不是公开稳定的 API 文档，可能随上游调整。查询失败、无权限、超时或响应格式变化时，页面显示“不可查询”及原因，不伪造额度，也不把未知写成额度充足。用量查询不发起模型推理、不写调用记录、不改变路由或付费开关；额度与套餐规则仍以 OpenCode 账号为准。

## 验证记录（2026-11-12）

使用用户提供的测试 Key，测试凭据仅保存在本地隔离的临时数据库，测试结束自动清理；没有写入代码、日志或版本库。通过真实 OpenCode Go 上游完成：

- 通过 SeekWell 的渠道模型查询，真实 `/v1/models` 返回 `deepseek-v4.1-flash`、`deepseek-v4-pro`。
- 从 SeekWell 入口分别调用 Flash 和 Pro，均返回 HTTP 200 和非空输出。
- Flash 的 SSE 流式输出返回数据和 `[DONE]`。
- Flash 发起工具调用，回传工具结果，再次请求后模型使用结果完成回答。
- 客户端传入 DSH 会话头和不传会话头的请求均成功。
- 在独立临时配置和目录中，使用 OpenCode CLI 1.17.11 经 SeekWell 调用 Flash，完成代码回答；随后通过真实 `read` / `glob` 工具读取测试文件并返回正确内容。验证发现该客户端使用 `X-Session-ID`；适配后重新运行，所有网关调用均返回 HTTP 200。

这些验证不代表所有账号、地区、额度、协议或图片输入都可用。Anthropic Messages、Responses、官方 Search / Fetch、真实 DSH 客户端与 OpenCode Go 的组合未在本次使用此 Key 的测试中验证。OpenCode Go 的 Flash 按当前官方文档使用 Chat Completions；其他接口不能仅凭这次测试声称可用。
