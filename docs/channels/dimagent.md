# DimAgent 渠道

SeekWell 可以通过本机 CLI 授权或用户自建的 API token，把 dimagent.cn 当作一个上游来源，使用该账号的 Credits 调用其提供的 DeepSeek 模型。渠道类型选择 `DimAgent`，API 根地址默认为 `https://dimagent.cn/v1`。

> **已做真实账号验证，自动刷新仍待自然过期验收。** 2026-11-13 使用 CLI 0.5.27 和 Nano 套餐验证模型目录、非流式、流式以及工具调用续答。结果与账号、套餐和上游策略有关。

## 令牌获取

推荐使用 **本机 CLI 授权**：安装官方 `dimcode` CLI，使用 `dim auth login` 完成浏览器或设备授权，在渠道的凭据方式中选择「使用本机 DimAgent 授权」。SeekWell 读取 CLI 的 `$DIMCODE_HOME/auth.json`（默认 `~/.dimcode/v2/auth.json`），令牌临近过期时调用 `dim auth refresh`，不直接写授权文件。SeekWell 须与 CLI 使用相同系统账号、`DIMCODE_HOME`，且服务进程的 PATH 必须能找到 `dim`。

若账号的控制台设置中提供 API tokens，也可创建 token，选择「填 API Key」使用。不同套餐的权限可能不同，请以实际控制台为准。

CLI OAuth 凭据受官方客户端限制：该模式按 CLI 0.5.27 的公开请求格式发送客户端标识，模型目录请求带 `?type=dim`。上游政策或客户端协议变化可能导致接入失效。

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

非目标：不通过 DimAgent 接收 GLM、Step、MiMo、Gemini 等非 DeepSeek 模型，不在 SeekWell 内发起 DimAgent 账号登录或查询 Credits 余额，不做跨协议转换。

## 验证记录

2026-11-13 使用真实 DimAgent 账号、CLI 0.5.27 和隔离的 SeekWell 实例验证：

- `dim auth login --device-login` 登录成功；SeekWell 正确识别本机授权，列表不回显令牌。
- 模型目录返回 `deepseek-v4.1-flash`；CLI OAuth 模式需要 `?type=dim` 和官方客户端请求标识。
- Chat Completions 非流式返回 200 且内容非空；流式返回 SSE 和 `[DONE]`。
- 一轮工具调用和回传工具结果后的续答均返回 200（测试工具结果为模拟数据）。
- `dim auth refresh` 命令返回成功，随后 `dim auth status` 仍显示已登录；当前令牌仍新鲜，CLI 不会实际换取新令牌。SeekWell 的自动刷新及并发控制已用模拟测试覆盖；真实令牌尚未自然到达刷新窗口，真实换取与刷新后请求仍待验收。
- Responses、Messages 和视觉能力尚未用真实上游验证。只有实测通过的能力才应勾选；付费渠道仍需明确允许才会参与自动路由。
