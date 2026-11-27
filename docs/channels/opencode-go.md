# OpenCode Go 渠道

SeekWell 可通过用户提供的 OpenCode Go API Key，调用该套餐当前提供的 DeepSeek 与 GLM 模型。渠道类型选择 `OpenCode Go`，API 根地址默认是 `https://opencode.ai/inference/go/v1`。统一模型名可与上游模型 ID 不同：例如使用 `deepseek-flash` 作为统一名，将上游模型配置为 `deepseek-v4.1-flash`；GLM 同理，可用 `glm-5.3` 统一名映射到上游 `glm-5.3`。一个渠道对应一条模型映射，需要多个模型时建多条渠道。

OpenCode Go 是当前唯一允许配置 GLM 的渠道类型；DeepSeek 官方、DimAgent 与兼容 API 渠道仍只接受 DeepSeek。上游模型、区域、套餐资格、额度与条款可能改变，应以 [OpenCode 官方 Go 文档](https://opencode.ai/docs/console/go/)、上游模型目录和实际账号为准。套餐额度可能超出后产生费用，是否自动调用该渠道由用户自己勾选。

根据官方要求，SeekWell 用自己的 `SeekWell/0.1` 标识请求，并向 OpenCode Go 发送 `x-opencode-session`。优先使用客户端传入的 OpenCode、SeekWell、`X-Session-ID` 或 DSH 会话头；客户端没有会话头时为**每次请求**创建一个随机标识，不将无关请求假装是同一会话。后者不能跨请求保持上游缓存。SeekWell 的进程内会话亲和在有客户端会话头时生效，绑定一小时，重启后失效。

## GLM 模型

OpenCode Go 当前公开模型目录（2026-11-27 核实）提供以下 GLM 项；上游未来新增型号时，SeekWell 的目录查询会如实展示，不在代码中硬编码只允许 5.3：

| 统一模型名示例 | 上游模型 ID | 输入 | 上下文 / 最大输出 | 备注 |
|---|---|---|---|---|
| `glm-5.3` | `glm-5.3`（canonical `zhipuai/glm-5.3`） | 文本 | 1M / 131,072 | 支持工具调用、推理、结构化输出 |
| `glm-5.3-flash` | `glm-5.3-flash`（canonical `zhipuai/glm-5.3-flash`） | 文本、图片、视频 | 1M / 131,072 | 支持工具调用、推理、结构化输出 |

`glm-5.4` 目前不在 OpenCode Go 的公开模型目录中，智谱官方也说明其 API 尚待安全评估后开放；SeekWell 不会展示或宣称 5.4 已可用。未来上游返回 5.4 时目录可以展示、渠道也可以配置 `glm-` 名称，但在智谱官方价目发布前费用会显示未知。

### 能力与协议

模型目录只说明上游提供该模型，不代表某个具体账号一定拥有额度，也不自动验证协议能力。GLM-5.3 为文本模型，GLM-5.3-Flash 的上游元数据还包含图片 / 视频输入；Responses、Messages、工具调用、流式与图片是否可用仍应在真实账号中确认，再按需勾选 SeekWell 的能力开关。未勾选的能力不会路由到该渠道；SeekWell 不做跨协议转换。

### 计价

OpenCode Go 模型目录中的价格是美元报价，仅供展示上游目录，**不用于 SeekWell 的费用估算**。SeekWell 按智谱开放平台官方 API 价目以 CNY / 百万 tokens 估算：

| 模型 | 输入（缓存未命中） | 输出 | 缓存命中 | 时段 |
|---|---:|---:|---:|---|
| GLM-5.3 | 8 | 28 | 2 | 不分时段 |
| GLM-5.3-Flash | 0.8 | 2.8 | 0.23 | 不分时段 |

来源：[智谱官方 API 定价页](https://docs.bigmodel.cn/cn/guide/start/pricing)（2026-11-27 核实）。官方 API 价目没有高峰 / 空闲折扣，调用记录显示「不分时段」；缓存存储当前限时免费，SeekWell 不计存储费用。GLM 价目是随 SeekWell 版本发布的智谱官方内置快照，不参与 DeepSeek 的自动价格同步；智谱调价后需要发布新版本更新。实际扣费以 OpenCode Go / 智谱账单为准。

## 用量查询

渠道列表加载后，SeekWell 会自动只读请求 `GET https://opencode.ai/zen/go/v1/usage`，展示上游返回的 5 小时、每周、每月窗口已用百分比与重置时间；缺失的窗口不会补成 0。仅当渠道 BaseURL 的 host 为 `opencode.ai` 且使用 HTTPS 时才发送 API Key；自定义兼容网关显示为不支持查询，SeekWell 不会把 Key 发往该地址。

该端点来自 CLI 的账号用量实现，不是公开稳定的 API 文档，可能随上游调整。查询失败、无权限、超时或响应格式变化时，页面显示“不可查询”及原因，不伪造额度，也不把未知写成额度充足。页面上的用量查询不发起模型推理、不写调用记录，也不改变路由或付费开关。渠道因 HTTP 402 被标记额度耗尽后会暂时跳过；后台使用同一只读用量接口按重置时间或探测间隔确认恢复，确认前不会重新参与路由。额度与套餐规则仍以 OpenCode 账号为准。

## 验证记录（2026-11-12）

以下记录只覆盖 DeepSeek 模型；GLM 的真实账号调用尚未在本 Issue 中完成，公开目录只能证明上游提供该模型，不能证明具体套餐额度或协议能力。

使用用户提供的测试 Key，测试凭据仅保存在本地隔离的临时数据库，测试结束自动清理；没有写入代码、日志或版本库。通过真实 OpenCode Go 上游完成：

- 通过 SeekWell 的渠道模型查询，真实 `/v1/models` 返回 `deepseek-v4.1-flash`、`deepseek-v4-pro`。
- 从 SeekWell 入口分别调用 Flash 和 Pro，均返回 HTTP 200 和非空输出。
- Flash 的 SSE 流式输出返回数据和 `[DONE]`。
- Flash 发起工具调用，回传工具结果，再次请求后模型使用结果完成回答。
- 客户端传入 DSH 会话头和不传会话头的请求均成功。
- 在独立临时配置和目录中，使用 OpenCode CLI 1.17.11 经 SeekWell 调用 Flash，完成代码回答；随后通过真实 `read` / `glob` 工具读取测试文件并返回正确内容。验证发现该客户端使用 `X-Session-ID`；适配后重新运行，所有网关调用均返回 HTTP 200。

这些验证不代表所有账号、地区、额度、协议或图片输入都可用，也不代表 GLM 已通过真实调用验收。Anthropic Messages、Responses、官方 Search / Fetch、真实 DSH 客户端与 OpenCode Go 的组合未在本次使用此 Key 的测试中验证；GLM-5.3 / GLM-5.3-Flash 的模拟路由与计价测试不能替代真实 OpenCode Go 调用。OpenCode Go 的 Flash 按当前官方文档使用 Chat Completions；其他接口不能仅凭这次测试声称可用。
