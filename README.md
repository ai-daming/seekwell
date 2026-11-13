# SeekWell

SeekWell 是一个 DeepSeek 专用的本地模型网关。当前开发版提供渠道配置、OpenAI 兼容模型转发，以及 DeepSeek 官方 Search / Fetch 的转发入口。项目仍在首版开发中，OpenCode Go 的真实调用已完成初步验证，其余渠道和完整产品验收尚待验证。

## 已实现

- 为使用者创建独立访问 Key，支持启停，并按 Key 统计请求和上游返回的 Token 用量。完整 Key 仅创建时显示一次。

- 管理界面顶栏用图标切换「跟随系统 / 浅色 / 深色」主题：首次访问按系统配色显示，选择保存在浏览器本地，不改变服务端行为。

- 在管理界面提供只读的「使用统计」页面：展示全部访问 Key 的累计请求、成功 / 失败、Token、用量返回覆盖、累计花费和 Key 数量，逐个 Key 展示明细与花费，并显示最近 7 个本地自然日的请求、Token 与费用趋势。页面同时展示当前官方价目和价格历史；费用按 DeepSeek 官方公开单价估算，只汇总可计价请求。

- 在总览和渠道管理页自动只读查询 OpenCode Go 与 DimAgent 的上游额度 / 用量：OpenCode Go 展示 5 小时、每周、每月窗口的已用百分比与重置时间，DimAgent 展示套餐 Credits、到期时间和窗口 token 用量。查询不发起模型推理、不写调用记录，也不改变路由或付费开关。

- 在管理界面添加、编辑、启停和删除 DeepSeek 官方、OpenCode Go、DimAgent 或兼容 API 渠道；每个配置对应一个账号及一组模型映射。可以查询上游当前的 DeepSeek 模型目录，也可以按渠道发起一次最小真实补全测速，查看总耗时与上游状态码；付费渠道测速前需确认费用。可按用户确认的工具调用、图片、Responses 和 Anthropic Messages 能力筛选渠道。
- 将同名统一 DeepSeek 模型按渠道优先级转发到上游；上游认证失败、限流、服务错误或连接失败时，在开始向客户端输出前尝试下一个渠道。
- 转发 OpenAI Chat Completions 与 Responses；流式响应原样转发。上游是否支持对应协议及功能由用户确认。支持通过 `X-OpenCode-Session`、`X-SeekWell-Session-Id`、`X-Session-ID` 或 DSH 的 `X-DeepSeek-Harness-Session-Id` 保持进程内会话亲和；绑定一小时后过期，重启不保留。
- 对支持该协议并已配置 Messages 地址的渠道原样转发 Anthropic Messages，不做跨协议转换。
- 转发 DeepSeek 官方 Search / Fetch，并可在管理界面测试搜索。搜索使用渠道类型为「DeepSeek 官方」且允许自动调用的账号。
- 付费渠道默认不参与自动调用；在渠道配置中明确启用后才参与。管理界面「调用记录」按尝试展示请求的渠道、渠道类型、状态、上游返回的输入 / 输出 Token、缓存命中率、估算费用及高峰 / 空闲时段；非官方渠道标注「官方价折算」，缺缓存拆分标注「按未命中估」，无法计价时显示具体原因。不保存请求正文。

- 费用按 DeepSeek 官方公布的 CNY / 百万 tokens 单价计算：北京时间周一至周五 09:00–12:00、14:00–18:00 为高峰，周末与中国法定节假日全天空闲。所有渠道的成功请求都按统一后的官方模型价推算；非官方渠道得到的是官方等价参考，不是实际扣费。上游没有缓存命中 / 未命中拆分时，全部输入按未命中单价估算。服务内置价格快照，并会从官方定价文档自动同步，记录首次观察到每次价目变化的时间；同步失败时保留上一份快照并在页面提示。

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

在界面添加渠道，填写包含 `/v1` 的 API 根地址、凭据、统一模型名和上游模型名（DimAgent 使用本机 CLI 授权，不需要 API Key）。OpenCode Go 与 DimAgent 均可直接选择对应渠道类型，详见 [OpenCode Go 渠道说明](docs/channels/opencode-go.md) 与 [DimAgent 渠道说明](docs/channels/dimagent.md)。名称都应包含 `deepseek`。根据真实上游确认并勾选工具调用、图片、Responses 和 Messages 能力，未勾选时对应请求不会被路由到该渠道。同一个统一模型可以配置多个渠道，数字较小的优先。官方搜索和付费模型如需自动调用，要勾选「允许自动使用此付费渠道」。

在管理界面的「访问 Key」中创建 Key，客户端使用该 Key 访问 `/v1`。

客户端示例（请替换 Key 和模型名）：

```bash
curl http://127.0.0.1:8350/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -H 'Authorization: Bearer YOUR_SEEKWELL_KEY' \
  -d '{"model":"deepseek-flash","messages":[{"role":"user","content":"你好"}]}'
```

搜索示例：

```bash
curl http://127.0.0.1:8350/v1/tools/search \
  -H 'Content-Type: application/json' \
  -H 'Authorization: Bearer YOUR_SEEKWELL_KEY' \
  -d '{"query":"DeepSeek","count":5}'
```

## 当前限制

- Token 用量仅统计上游返回的数据；没有返回、无法解析或非流式响应体超过 2MB 解析上限时显示未知。
- 费用是公开单价估算，不是官方账单，也不含折扣、余额、税费或优惠；不包含 Search / Fetch 的按次费用。所有渠道（含非官方）都按 DeepSeek 官方公开单价折算，非官方渠道显示的是官方等价参考，不是渠道实际扣费；缺缓存拆分时按全部输入未命中估算，可能高估。失败请求、缺少 input/output 用量或模型不在价目表时费用未知，不按 0 计，也不计入合计。功能上线前的历史记录费用未知，不做猜测性回填。节假日表随应用发布更新，未覆盖当前年份时页面会提示。实际扣费以各渠道账单为准。
- 渠道上游模型名会按官方名归一再计价：`deepseek-v4.1-flash`、`deepseek-v4.1-flash-1017`、`deepseek-v4-flash`、`deepseek-v4-flash-vision-exp` 按 `deepseek-flash` 计价，`deepseek-pro`、`deepseek-v4.1-pro` 按 `deepseek-v4-pro` 计价，并处理 `deepseek/` 前缀。未收录的新上游 ID 会保持未知，不做模糊匹配或用另一个模型猜价。
- 价格历史只记录本系统首次观察到的新价目，观察时间不是官方精确生效时间，最早只能到首次成功同步或升级时已有的远程快照；官方页不提供旧价，因此无法补出更早历史。功能上线前已为未知的历史请求不会回溯补算。同一价目的重复同步不重复记录，同步失败不产生新记录。
- 使用统计只统计携带有效访问 Key 的 `/v1` POST 请求；认证失败的请求无法归属到 Key，不计入统计；管理界面的模型测试、测速和搜索测试也不计入。
- 使用统计不做自定义时间范围、导出、按模型或渠道拆分，也不读取或关联历史 `requests` 调用记录。

- WorkBuddy 等产品账号的直接登录，以及 OpenCode 的账号登录尚未实现。OpenCode Go 支持直接使用 API Key 和只读额度查询。若已有兼容 API 网关，可以作为兼容渠道填写，但具体模型和协议需要实测。
- 尚不支持客户端搜索工具的自动转换、模型能力自动发现或跨渠道文件 / 会话转换。模型目录查询仅展示上游 ID，不会自动验证或建立映射。Responses 和 Messages 只是向支持该协议的上游接口透传，不是跨协议转换；原生文件接口还未实现。
- 渠道能力、真实版本和上下文不会自动验证；能力开关由用户根据渠道实际情况确认，配置同名模型也意味着用户自行确认版本兼容。未实现限流冷却或额度优先。会话亲和只基于请求头和进程内状态，不负责转换上游专有会话或文件 ID。
- 已开始输出的流中断不会自动拼接其他渠道的输出；尝试记录包含模型、渠道、渠道类型、状态、上游返回的输入 / 输出 Token、缓存命中率、估算费用与时段和耗时，不包含搜索记录或 Search / Fetch 的按次费用。
- 管理界面无内置访问认证，只应在受信任的本机环境运行；服务未设计为直接暴露到公网。凭据存储在本地 SQLite 中，未加密。
- OpenCode Go 和 DimAgent 的真实 API 验证见各自渠道说明；DimAgent 的真实令牌自动刷新尚待自然过期验收。尚无 DeepSeek 官方 API、OpenDesign、WorkBuddy 或真实 DSH 客户端的端到端验收。模拟上游通过不等于这些渠道验收通过。

整体方案与仍待确认的决策见 [设计文档](docs/design.md)。
