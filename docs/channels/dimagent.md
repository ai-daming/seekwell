# DimAgent 渠道

SeekWell 可以通过用户自建的 DimAgent API Key，把 dimagent.cn 当作一个上游来源，使用该账号的 Credits 调用其提供的 DeepSeek 模型。渠道类型选择 `DimAgent`，API 根地址默认为 `https://dimagent.cn/v1`。

> **未用真实令牌验证。** 本文的地址、模型 ID 与能力说明来自公开文档和匿名探测；SeekWell 侧只用模拟上游做了自动化测试。真实账号下的模型目录、流式、工具调用与协议可用性，请按下方清单自行确认。

## 令牌获取

在 dimagent.cn 的账户中心进入 **我的令牌 / My API Keys**，新建一个长期 API Key（通常以 `sk-` 开头）。SeekWell 使用这个 Key 作为 `Authorization: Bearer` 凭据，不需要 DimAgent 官方 CLI 的 OAuth 登录。

## 基础配置

| 配置项 | 建议值 |
|---|---|
| 渠道类型 | `DimAgent` |
| API 地址 | `https://dimagent.cn/v1` |
| 统一模型名 | `deepseek-flash` |
| 上游模型名 | `deepseek-v4.1-flash` |
| 能力 | 只勾选「工具调用」，其余留空 |

切换渠道类型时，界面会自动填入上述地址和上游模型名；统一模型名保持 `deepseek-flash` 不变。

## 模型映射

DimAgent 的模型目录同时包含 DeepSeek 与非 DeepSeek 模型。SeekWell 目前只接受名称包含 `deepseek` 的模型，因此只应映射 DeepSeek 项。一个渠道对应一条模型映射；需要多个上游模型时，建多条渠道或调整映射：

- Flash：统一名 `deepseek-flash` → 上游 `deepseek-v4.1-flash`
- Pro：统一名 `deepseek-pro` → 上游 `deepseek-v4-pro`
- 视觉模型 `deepseek-v4.1-flash-1017`：如确有需要，单独建一条映射；视觉能力是否可用见下方待实测项

上游模型 ID 是否带 `deepseek/` 前缀（例如 `deepseek/deepseek-v4.1-flash`）只有在真实账号的「查询模型」结果里才能确认。SeekWell 允许映射值包含 `/`，请以目录返回值为准。

## 计费与自动调用

DimAgent 使用预付 Credits，余额只在 dimagent.cn 账户中心查看。新渠道默认是付费渠道，且「允许自动使用此付费渠道」未勾选；不勾选时请求不会自动路由到该渠道，勾选后才参与自动调用。SeekWell 不查询余额、不记录费用。

## 能力待实测

默认只勾选工具调用。以下能力在真实 DimAgent 上游尚未确认，未勾选时对应请求不会路由到该渠道：

- **Responses**：`/v1/responses` 是否按 OpenAI Responses 语义可用。
- **Messages**：`/v1/messages` 是否按 Anthropic Messages 语义可用；勾选时必须填写 Messages API 地址，界面上通常可直接填 `https://dimagent.cn/v1`。
- **视觉**：`deepseek-v4.1-flash-1017` 是否接受图片输入。
- **流式**：Chat Completions 的 SSE 与 `[DONE]` 是否正常。

## Search / Fetch 不适用

DimAgent 没有与 DeepSeek 官方一致的 `/v1/tools/search`、`/v1/tools/fetch` 接口。SeekWell 的 Search / Fetch 仍然只使用渠道类型为「DeepSeek 官方」的渠道；把 DimAgent 配成该类型不会让搜索走它。

非目标：不通过 DimAgent 接收 GLM、Step、MiMo、Gemini 等非 DeepSeek 模型，不做 DimAgent 账号登录或 Credits 余额查询，不做跨协议转换。

## 验证记录

**未用真实令牌验证。** 自动化测试只覆盖渠道类型读写、路由门禁、模型改写、模型目录过滤与 Search 隔离，使用本地模拟上游；不代表真实 DimAgent 上游可用。

有令牌后建议依次确认：

1. 点击「查询模型」，记录返回的 DeepSeek 模型 ID，确认是否需要 `deepseek/` 前缀，必要时修正本文示例与渠道映射。
2. 用非流式 Chat Completions 请求 `deepseek-flash`，确认 HTTP 200 且返回内容非空。
3. 用流式请求确认收到 SSE 数据与 `[DONE]`。
4. 发起一轮带工具调用的请求并回传工具结果，确认模型能使用结果继续回答；通过后保留「工具调用」勾选。
5. 按需试 Responses、Messages 与视觉模型；只有实测通过的项才勾选对应能力。
6. 如果希望该渠道参与自动路由，再勾选「允许自动使用此付费渠道」。

把实测结论补进本段后，再关闭对应 issue；在此之前，请把本文和界面上的能力提示都视为未确认状态。
