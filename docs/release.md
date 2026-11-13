# 发布流程

本流程用于创建正式版本，让管理界面左下角显示当前提交对应的 GitHub Release 版本号和发布日期（北京时间）。界面只显示部署产物在构建时写入的版本，不在浏览器里查询 GitHub。

## 1. 准备

1. 确认要发布的代码已经合入 `main`，并在该提交上通过测试：

   ```bash
   make test
   ```

2. 确定版本 tag，例如 `v0.5.0`。tag 必须使用 `vMAJOR.MINOR.PATCH` 形式，可以带预发布后缀；正式构建会识别 `vX.Y.Z` 和 `vX.Y.Z-suffix`。
3. 发布必须使用干净的提交。正式构建会检查工作区，有未提交改动时直接失败，避免把未提交代码标成某个 Release。

## 2. 创建 GitHub Release

1. 在 GitHub 上针对将要发布的那个提交创建 tag 和 Release。
2. 确认 Release 不是 draft。正式构建会排除 draft；找不到有效 Release 时直接失败。
3. 不要移动已有的 tag，也不要为了复用旧版本号改写已发布的 Release。
4. 界面日期来自 Release 的 `published_at`（实际发布时间），不是 tag 创建时间、提交时间或 `package.json` 的 `version`。日期固定按 `Asia/Shanghai` 转换。
5. 本流程不单独排除 prerelease：只要 Release 不是 draft 且 `published_at` 有效，就可以用于正式构建。团队如需只发布稳定版，应在创建 Release 时自行约定。

## 3. 检出并构建

1. 拉取 tag，并检出与 Release 对应的提交：

   ```bash
   git fetch --tags
   git checkout <version-tag>
   git rev-parse HEAD
   ```

2. 确认当前 `HEAD` 与 GitHub Release 指向的提交一致。本地 tag 过期、远程 tag 指向其他提交时，正式构建会失败。
3. 运行测试并构建正式版本：

   ```bash
   make test
   make release
   ```

   `make release` 会先校验当前 `HEAD` 对应的 Release，再安装前端依赖、执行正式前端构建，最后构建 Go 后端。前端和后端来自同一个 `HEAD`，任何一步失败都会停止。

4. 正式构建会联网读取 GitHub Release。未认证请求有速率限制；可以设置 `GITHUB_TOKEN` 或 `GH_TOKEN` 提高限额：

   ```bash
   GITHUB_TOKEN=... make release
   ```

   可用环境变量：

   | 变量 | 用途 |
   | --- | --- |
   | `GITHUB_TOKEN` / `GH_TOKEN` | 可选的 GitHub API token；没有 token 时按未认证请求 |
   | `SEEKWELL_REPO` | 覆盖仓库 slug，格式 `owner/repo`；默认从 `origin` URL 解析 |
   | `SEEKWELL_RELEASE_REMOTE` | 覆盖校验远程 tag 的远端名，默认 `origin` |

   正式构建开始时会清空 `apps/web/dist`；校验或构建失败后不会留下可以被误认为本次发布的旧产物。

## 4. 部署

1. 部署前端 `apps/web/dist` 和本次 `make release` 构建出的后端二进制。
2. 前端和后端必须来自同一次 `make release`、同一个 `HEAD`。不要混用新前端和旧后端，也不要只更新其中一边。
3. 部署前保留上一个由本流程构建的前端和后端产物，供回滚使用。

## 5. 验证

1. 打开部署后的管理界面，确认左下角显示 `vX.Y.Z · YYYY-MM-DD`，且版本和日期与本次 Release 一致。
2. 也可以在前端产物中确认该文字存在：

   ```bash
   grep -R "vX.Y.Z · YYYY-MM-DD" apps/web/dist
   ```

3. 如果界面显示的版本与本次 Release 不一致、仍是旧版或显示「开发版」，说明部署未生效或线上尚未升级。

## 6. 失败报告

部署后验证不通过时，必须如实报告「线上尚未升级 / 部署未生效」，不要因为构建成功就宣称发布完成。需要记录：

- 目标 Release 的 tag 和 `published_at`；
- 实际构建出的版本文字；
- 线上界面实际显示的版本文字；
- 已执行的部署步骤和失败原因。

构建成功只代表本地产物包含预期版本文字，不代表线上已经升级。

## 7. 回滚

1. 回滚时同时重新部署上一个版本的**前端和后端**产物，不要只回滚其中一边。
2. 部署旧产物后，界面会原样显示旧产物构建时写入的版本号和日期，不会重新查询 GitHub Release。
3. 从旧 tag 重新执行 `make release` 也能得到该 tag 对应的 Release 信息，前提是该 tag 上的代码已经包含本功能。
4. `v0.4.0` 及更早的既有产物构建于本功能上线之前，产物中没有版本注入逻辑，回滚到这些旧产物不会显示对应版本。它们不能作为「回滚后显示旧版本号」的验证对象。真实回滚验收需要两个由本流程构建的版本，或用测试 fixture 模拟两个产物。
5. 本仓库当前没有部署脚本或线上地址；实际部署和回滚验证由具备部署环境的人执行。

## 8. 开发版

`npm run dev` 或普通 `npm run build` 显示「开发版」，不显示日期。这是预期行为，不是缺少 Release：

- `npm run dev` 和 `npm run build` 不查询 Git 或 GitHub，也不要求 Release 网络；
- 只有显式的 `npm run build:release` / `make release` 才读取当前提交对应的 Release；
- `make web` 仍是普通构建，显示「开发版」。

可以用下面的测试验证普通构建和正式注入：

```bash
cd apps/web
node --test test/version.test.mjs
```

## 9. 故障排查

| 现象 | 原因与处理 |
| --- | --- |
| `当前 HEAD ... 没有 tag` | 当前提交上没有 tag。确认 Release 是在这个提交上创建，然后执行 `git fetch --tags`。正式构建不会改用其他提交或 GitHub 最新 Release。 |
| `tag ... 没有对应的 Release` / API 404 | 先创建 Release，确认不是 draft；确认 tag 已推送到远程。 |
| `Release 仍是 draft` | 在 GitHub 上发布 Release 后重试。 |
| `published_at` 缺失或非法 | 修好 Release 的发布时间后重试；不要手工改界面文字。 |
| 远程 tag 与 HEAD 不一致 | 本地 tag 过期或 tag 被移动。执行 `git fetch --tags`，检出正确提交；必要时重新创建 tag/Release。 |
| 工作区有未提交改动 | 提交或清理改动后重试。正式构建不使用未提交代码。 |
| GitHub API 401/403 或限流 | 检查 `GITHUB_TOKEN`/`GH_TOKEN`，稍后重试。失败时不要回退成「开发版」发布。 |
| 网络错误 | 检查到 `api.github.com` 的连接后重试；正式构建不会静默生成猜测版本。 |
| 日期差一天 | 确认看的是 `published_at` 的北京时间日期。`Intl.DateTimeFormat` 固定按 `Asia/Shanghai` 转换，不受构建机时区影响。 |

## 10. 边界说明

- 正式构建只使用精确指向当前 `HEAD` 的 tag 和该 tag 对应的 Release；绝不使用 `releases/latest`、最近的 tag、提交时间或 `package.json` 版本。
- 同一提交有多个 tag 时，会选择最高版本；无法唯一排序时失败。
- 版本文字只出现在现有左下角 `.version` 区域，不新增接口、页面或运行时请求。
- 本功能上线前的旧产物无法追溯显示版本号；需要重新以新 tag 发布才能让界面显示新版本。
