import { test } from 'node:test';
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { existsSync, readFileSync, readdirSync, statSync } from 'node:fs';
import { dirname, join, relative } from 'node:path';
import { fileURLToPath } from 'node:url';

const webRoot = join(dirname(fileURLToPath(import.meta.url)), '..');
const srcRoot = join(webRoot, 'src');
const componentsRoot = join(srcRoot, 'components');

function walk(dir) {
  return readdirSync(dir).flatMap((name) => {
    const path = join(dir, name);
    return statSync(path).isDirectory() ? walk(path) : [path];
  });
}

function read(path) {
  return readFileSync(path, 'utf8');
}

const sourceFiles = existsSync(srcRoot) ? walk(srcRoot) : [];
const tsxFiles = sourceFiles.filter((file) => file.endsWith('.tsx'));
const cssFiles = sourceFiles.filter((file) => file.endsWith('.css'));
const componentFiles = existsSync(componentsRoot) ? walk(componentsRoot) : [];
const allSource = sourceFiles.map(read).join('\n');
const allCss = cssFiles.map(read).join('\n');
const componentSource = componentFiles.map(read).join('\n');
const copySource = existsSync(join(srcRoot, 'copy.ts')) ? read(join(srcRoot, 'copy.ts')) : '';
const apiSource = existsSync(join(srcRoot, 'api.ts')) ? read(join(srcRoot, 'api.ts')) : '';

test('T1 源码不引用外部资源', () => {
  assert.ok(cssFiles.length > 0, '缺少 CSS 文件');
  for (const file of cssFiles) {
    const css = read(file);
    for (const match of css.matchAll(/@import\s+[^;]+;/g)) {
      assert.ok(!/https?:|\/\//.test(match[0]), `${relative(webRoot, file)} 仍有外部 @import`);
    }
    for (const match of css.matchAll(/url\(\s*['"]?([^)'"\s]+)/g)) {
      assert.ok(!/^(https?:)?\/\//.test(match[1]), `${relative(webRoot, file)} 仍有外部 url()`);
    }
  }
  const html = read(join(webRoot, 'index.html'));
  for (const match of html.matchAll(/<(?:link|script)\b[^>]*\b(?:href|src)=["']([^"']+)/g)) {
    assert.ok(!/^(https?:)?\/\//.test(match[1]), `index.html 仍引用外部资源 ${match[1]}`);
  }
});

test('T2 图标位使用 Emoji 12.1 内的彩色 emoji', () => {
  const forbidden = ['\u2733', '\u25EB', '\u25C8', '\u2315', '\u2197', '\u25CC', '\u21BB', '\uFF0B', '\u00D7'];
  for (const file of tsxFiles) {
    const source = read(file);
    for (const char of forbidden) {
      assert.ok(!source.includes(char), `${relative(webRoot, file)} 仍有旧几何符号图标 U+${char.codePointAt(0).toString(16)}`);
    }
  }

  assert.match(copySource, /export const icons\s*=/, '图标应集中定义在 copy.ts');
  const iconBlock = copySource.match(/export const icons\s*=\s*\{([\s\S]*?)\}\s*(?:as const)?;/);
  assert.ok(iconBlock, '无法读取 icons 定义');
  const iconValues = [...iconBlock[1].matchAll(/:\s*'([^']+)'/g)].map((match) => match[1]);
  assert.ok(iconValues.length >= 18, `图标位不足：${iconValues.length}`);
  for (const icon of iconValues) {
    const chars = [...icon];
    for (let index = 0; index < chars.length; index += 1) {
      const char = chars[index];
      const codePoint = char.codePointAt(0);
      if (codePoint === 0xfe0f) continue;
      assert.ok(codePoint <= 0x1faff, `${icon} 超出 Emoji 12.1 范围`);
      assert.ok(!(codePoint >= 0x1faa0 && codePoint <= 0x1faff), `${icon} 含 Emoji 13+ 字符`);
      const presentation = /\p{Emoji_Presentation}/u.test(char);
      const next = chars[index + 1];
      assert.ok(presentation || next === '\ufe0f', `${icon} 的 U+${codePoint.toString(16)} 需要 U+FE0F`);
    }
  }
});

test('T3 管理界面接口集合', () => {
  const paths = new Set();
  for (const match of allSource.matchAll(/['"`](\/(?:api|v1)\/[^'"`]*)['"`]/g)) {
    paths.add(match[1].replace(/\$\{[^}]+\}/g, '${id}'));
  }
  assert.deepEqual([...paths].sort(), [
    '/api/channels',
    '/api/channels/${id}',
    '/api/channels/usage',
    '/api/channels/${id}/discover',
    '/api/channels/${id}/speedtest',
    '/api/channels/${id}/test',
    '/api/keys',
    '/api/keys/${id}',
    '/api/requests',
    '/api/tools/search',
    '/api/usage/stats',
  ].sort());
});

test('T4 copy 不变量非空且被组件引用', () => {
  const required = [
    'unverifiedAvailability',
    'enableIsNotAvailable',
    'channelNotice',
    'searchBillingNotice',
    'catalogUnverified',
    'keyKeptPlaceholder',
    'deleteConfirm',
    'fullObjectUpdate',
    'typeChangeReset',
    'paidAutoGuard',
    'messagesRequiredGuard',
    'statusZero',
    'searchCount',
    'emptyChannels',
    'speedTestConfirm',
    'speedTestNotice',
    'authModeCli',
    'dimAuthHint',
    'usageScopeNotice',
    'usageStatsEmptyKeys',
    'usageUnknown',
    'usageNeverUsed',
    'usageStatsFailed',
  ];
  for (const name of required) {
    const match = copySource.match(new RegExp(`\\b${name}\\s*:\\s*(['"\`])([^'"\`]+)\\1`));
    assert.ok(match && match[2].trim(), `copy.${name} 缺失或为空`);
    assert.ok(componentSource.includes(`copy.${name}`), `组件没有引用 copy.${name}`);
  }
  assert.ok(!/\/\/\s*TODO/.test(copySource), 'copy.ts 不应留下 TODO');
});

test('T5 交互守卫仍存在', () => {
  assert.match(componentSource, /window\.confirm/, '缺少删除二次确认');
  assert.match(componentSource, /type=["']password["']/, '缺少密码框');
  assert.match(componentSource, /copy\.keyKeptPlaceholder/, '密钥占位未引用 copy');
  assert.match(componentSource, /method:\s*['"]PUT['"]/, '缺少 PUT');
  assert.match(componentSource, /api_key\s*:\s*['"]['"]/, 'PUT 请求体必须显式带空 api_key');
});

test('T6 构建产物仍在 dist 根且无外部字体', () => {
  execFileSync('npm', ['run', 'build'], { cwd: webRoot, stdio: 'pipe' });
  const distRoot = join(webRoot, 'dist');
  assert.ok(existsSync(join(distRoot, 'index.html')), 'dist/index.html 缺失');
  const html = read(join(distRoot, 'index.html'));
  for (const match of html.matchAll(/<(?:link|script)\b[^>]*\b(?:href|src)=["']([^"']+)/g)) {
    assert.ok(!/^(https?:)?\/\//.test(match[1]), `构建产物引用外部资源 ${match[1]}`);
  }
  const distCss = walk(distRoot).filter((file) => file.endsWith('.css')).map(read).join('\n');
  assert.doesNotMatch(distCss, /fonts\.googleapis/, '构建产物仍包含 Google Fonts');
});

test('T7 依赖键与版本冻结', () => {
  const pkg = JSON.parse(read(join(webRoot, 'package.json')));
  assert.deepEqual(Object.keys(pkg.dependencies).sort(), ['react', 'react-dom']);
  assert.deepEqual(Object.keys(pkg.devDependencies).sort(), [
    '@types/react',
    '@types/react-dom',
    '@vitejs/plugin-react',
    'typescript',
    'vite',
  ]);
  assert.deepEqual(pkg.dependencies, { react: '19.3.1', 'react-dom': '19.3.1' });
  assert.deepEqual(pkg.devDependencies, {
    '@types/react': '19.3.0',
    '@types/react-dom': '19.3.0',
    '@vitejs/plugin-react': '6.3.0',
    typescript: '7.0.2',
    vite: '8.3.8',
  });
});

test('T8 空态、错误分支与溢出规则', () => {
  assert.match(componentSource, /copy\.emptyChannels/, '缺少 0 渠道空态');
  assert.match(componentSource, /emptyActivity|还没有调用记录/, '缺少 0 记录空态');
  assert.match(componentSource, /没有返回模型/, '缺少模型目录空态');
  assert.match(componentSource, /copy\.statusZero/, 'status=0 未走连接失败分支');
  assert.match(allCss, /overflow-wrap/, '缺少长文本换行规则');
  assert.match(allCss, /text-overflow/, '缺少长文本截断规则');
  assert.match(allCss, /overflow-y\s*:\s*auto/, '缺少记录表/JSON 滚动规则');
});

test('T9 保留 800px 与 450px 响应式断点', () => {
  assert.match(allCss, /@media\s*\(max-width:\s*800px\)/, '缺少 800px 断点');
  assert.match(allCss, /@media\s*\(max-width:\s*450px\)/, '缺少 450px 断点');
  assert.match(allCss, /\.sidebar[^{]*\{[^}]*width\s*:\s*var\(--sidebar-rail\)/s, '窄屏侧栏图标条宽度缺失');
  assert.match(allCss, /\.sidebar[^{]*\{[^}]*font-size\s*:\s*var\(--icon-size\)/s, '窄屏 emoji 字号缺失');
});

test('T10 页级错误与对话框内错误双通道', () => {
  assert.match(componentSource, /error\s*&&\s*!modal/, '缺少页级错误横幅');
  assert.match(componentSource, /<form[\s\S]*?error\s*&&[\s\S]*?<\/form>/, '缺少对话框内错误提示');
});

test('T11 时间、空耗时与非法日期安全', () => {
  assert.match(allSource, /toLocaleString\(\s*['"]zh-CN['"]\s*\)/, '时间未使用 zh-CN 本地化');
  assert.match(componentSource, /duration_ms\s*\?\?\s*0/, 'duration_ms 缺失时未回退 0');
  assert.match(componentSource, /try\s*\{[\s\S]*?new Date\([\s\S]*?catch/, '非法日期未做异常保护');
});

test('T12 窄屏图标导航仍保留可访问名称', () => {
  const shell = read(join(componentsRoot, 'Shell.tsx'));
  assert.match(shell, /aria-label=\{item\.label\}/, '导航按钮缺少 aria-label，窄屏隐藏文字后会失去可访问名称');
  assert.match(allCss, /\.nav-item__label[\s\S]*?display:\s*none/, '窄屏文字隐藏规则缺失');
});

test('T13 4xx/5xx 状态码按错误渲染', () => {
  const activity = read(join(componentsRoot, 'ActivityTable.tsx'));
  assert.match(
    activity,
    /status\s*===\s*0\s*\|\|\s*status\s*>=\s*400|status\s*>=\s*400\s*\|\|\s*status\s*===\s*0/,
    '仅 status=0 被标红，4xx/5xx 仍显示为成功',
  );
});

test('T14 静默刷新不卸载表格', () => {
  const app = read(join(componentsRoot, 'App.tsx'));
  assert.match(app, /function refresh\(silent\s*=\s*false\)/, 'refresh 缺少静默模式');
  assert.match(app, /if\s*\(!silent\)\s*setLoading\(true\)/, '静默刷新仍会显示加载态');
  assert.match(app, /refresh\(true\)/, '保存/启停/删除后未使用静默刷新');
});

test('T15 干净检出直接 make test 可跑前端', () => {
  const makefile = read(join(webRoot, '..', '..', 'Makefile'));
  assert.match(
    makefile,
    /web-test:\s*\n\tcd apps\/web && npm ci && npm run test/,
    'web-test 未安装依赖，干净检出会报 tsc: not found',
  );
  assert.match(
    makefile,
    /test:\s*\n\tcd apps\/server && go test \.\/internal\/core\n\t\$\(MAKE\) web-test/,
    'test 目标未复用带依赖安装的 web-test',
  );
});

test('T16 测速入口、付费确认与结果展示', () => {
  assert.match(apiSource, /speedTest:\s*\(id:\s*number\)[\s\S]*?\/speedtest/, '缺少测速 API 客户端');
  assert.match(componentSource, /channel\.paid\s*&&\s*!window\.confirm\(/, '付费渠道测速前缺少费用确认');
  assert.match(componentSource, /copy\.speedTestConfirm/, '测速确认未引用 copy');
  assert.match(componentSource, /speeding\s*===\s*channel\.id/, '测速按钮缺少进行中态');
  assert.match(componentSource, /测速中/, '测速按钮未显示进行中文案');
  assert.match(componentSource, /duration_ms/, '测速结果未展示耗时');
  assert.match(componentSource, /result\.status/, '测速结果未展示上游状态码');
});

test('T17 调用记录展示渠道类型且未知不猜', () => {
  const typeSource = read(join(srcRoot, 'types.ts'));
  assert.match(typeSource, /export type Entry\s*=\s*\{[\s\S]*?kind:\s*string;[\s\S]*?\};/, 'Entry 缺少 kind 字段');

  const kindBlock = typeSource.match(/export const kindLabel[\s\S]*?\n\};/);
  assert.ok(kindBlock, '无法读取 kindLabel 定义');
  assert.match(kindBlock[0], /kind === 'deepseek'\)\s*return 'DeepSeek 官方'/, 'deepseek 文案缺失');
  assert.match(kindBlock[0], /kind === 'opencode_go'\)\s*return 'OpenCode Go'/, 'opencode_go 文案缺失');
  assert.match(kindBlock[0], /kind === 'compatible'\)\s*return '兼容 API \/ 网关'/, 'compatible 文案缺失');
  assert.match(kindBlock[0], /return '未知'/, '空值或未知类型未显示「未知」');

  const activity = read(join(componentsRoot, 'ActivityTable.tsx'));
  assert.match(
    activity,
    /<th>时间<\/th>\s*<th>统一模型<\/th>\s*<th>渠道<\/th>\s*<th>渠道类型<\/th>\s*<th>状态码<\/th>\s*<th>输入 tokens<\/th>\s*<th>输出 tokens<\/th>\s*<th>缓存命中率<\/th>\s*<th>耗时<\/th>/,
    '调用记录表头缺少「渠道类型」「Token」或顺序不对',
  );
  assert.match(activity, /kindLabel\(entry\.kind\)/, '调用记录未复用 kindLabel');

  const channels = read(join(componentsRoot, 'ChannelsTable.tsx'));
  assert.match(channels, /kindLabel\(channel\.kind\)/, '渠道管理页未复用 kindLabel');
});

test('T18 模型测试入口、付费确认与结果展示', () => {
  const shell = read(join(componentsRoot, 'Shell.tsx'));
  assert.match(shell, /id:\s*'test'[\s\S]*?label:\s*'模型测试'/, '侧栏缺少模型测试 tab');
  const app = read(join(componentsRoot, 'App.tsx'));
  assert.match(app, /ModelTestPanel/, '模型测试页面未接入 App');
  assert.match(apiSource, /testModel:\s*\(id:\s*number[\s\S]*?\/api\/channels\/\$\{id\}\/test/, '缺少模型测试 API 客户端');
  assert.match(componentSource, /channel\.paid\s*&&\s*!window\.confirm\(/, '付费渠道模型测试前缺少费用确认');
  assert.match(componentSource, /copy\.modelTestConfirm/, '模型测试确认未引用 copy');
  assert.match(componentSource, /请求中/, '模型测试按钮未显示进行中文案');
  assert.match(componentSource, /copy\.modelTestUnmapped/, '未映射模型缺少标注');
  assert.match(copySource, /未加入路由配置/, '未映射模型标注文案缺失');
  assert.match(componentSource, /JSON\.stringify\([\s\S]*?null,\s*2\)/, '结果区缺少 Pretty JSON 展示');
  assert.match(componentSource, /copy\.modelTestNotJSON/, '非 JSON 响应缺少标注');
});

test('T19 DimAgent 不提供 API Key 入口', () => {
  const dialog = read(join(componentsRoot, 'ChannelDialog.tsx'));
  const app = read(join(componentsRoot, 'App.tsx'));
  assert.match(dialog, /draft\.kind\s*===\s*'dimagent'/, 'DimAgent 缺少固定授权展示分支');
  assert.match(dialog, /copy\.authModeCli/, 'DimAgent 固定展示未引用 copy.authModeCli');
  assert.match(dialog, /copy\.dimAuthHint/, 'DimAgent 授权提示未引用 copy.dimAuthHint');
  assert.doesNotMatch(dialog, /填 API Key/, 'DimAgent 仍出现「填 API Key」文案');
  assert.doesNotMatch(dialog, /<option[^>]*value=["']api_key["']/, '仍存在 API Key 凭据选项');
  assert.doesNotMatch(dialog, /凭据方式[\s\S]{0,120}<select/, 'DimAgent 仍渲染凭据方式下拉');
  assert.match(app, /auth_mode:\s*dimAgent\s*\?\s*'cli_oauth'\s*:\s*'api_key'/, 'changeKind 未按渠道类型设置凭据方式');
  assert.match(app, /channel\.kind === 'dimagent' \? 'cli_oauth' : channel\.auth_mode/, '编辑 DimAgent 未固定 cli_oauth');
  assert.match(app, /channel\.kind === 'dimagent'[\s\S]{0,160}api_key: ''/, '编辑 DimAgent 未清空 API Key');
});

test('T20 渠道用量/额度接口与展示分支', () => {
  const typeSource = read(join(srcRoot, 'types.ts'));
  const channels = read(join(componentsRoot, 'ChannelsTable.tsx'));
  const app = read(join(componentsRoot, 'App.tsx'));
  assert.match(apiSource, /channelUsage:\s*\(\)[\s\S]*?\/api\/channels\/usage/, '缺少渠道用量 API 客户端');
  assert.match(typeSource, /export type ChannelUsage\s*=/, '缺少 ChannelUsage 类型');
  assert.match(typeSource, /export type UsageWindow\s*=/, '缺少 UsageWindow 类型');
  assert.match(app, /api\.channelUsage\(\)/, 'App 未单独请求渠道用量');
  assert.match(app, /function refreshUsage\(/, 'App 缺少独立用量刷新函数');
  assert.match(channels, /<th>用量 \/ 额度<\/th>/, '渠道表缺少「用量 / 额度」列');
  assert.match(channels, /copy\.usageLoading/, '缺少查询中分支');
  assert.match(channels, /copy\.usageUnavailable/, '缺少不可查询分支');
  assert.match(channels, /copy\.usageUnsupported/, '缺少未接入/不支持分支');
  assert.match(channels, /status\s*===\s*'unsupported'/, '缺少 unsupported 分支');
  assert.match(channels, /status\s*===\s*'error'/, '缺少 error 分支');
  assert.match(channels, /five_hour/, '缺少 OpenCode Go 窗口分支');
  assert.match(channels, /credits/, '缺少 DimAgent Credits 分支');
  assert.match(channels, /copy\.usageFiveHour/, 'OpenCode 5 小时窗口未引用 copy');
  assert.match(channels, /copy\.usageWeekly/, 'OpenCode 每周窗口未引用 copy');
  assert.match(channels, /copy\.usageMonthly/, 'OpenCode 每月窗口未引用 copy');
  assert.match(channels, /copy\.usageCredits/, 'DimAgent Credits 未引用 copy');
  assert.match(channels, /copy\.usageUnlimited/, 'DimAgent 无限额度未引用 copy');
  assert.match(channels, /toLocaleString\(\s*['"]zh-CN['"]\s*\)/, '用量日期未使用中文区域格式化');
  assert.match(channels, /slice\(0,\s*\d+\)/, '错误原因未截断');
});

test('T21 使用统计页面与接口', () => {
  const typeSource = read(join(srcRoot, 'types.ts'));
  const shell = read(join(componentsRoot, 'Shell.tsx'));
  const app = read(join(componentsRoot, 'App.tsx'));
  const usage = read(join(componentsRoot, 'UsagePage.tsx'));
  const usageCopy = read(join(srcRoot, 'copy.ts'));
  assert.match(typeSource, /export type Tab\s*=[^;]*'usage'/, 'Tab 缺少 usage');
  assert.match(typeSource, /export type UsageStats\s*=/, '缺少 UsageStats 类型');
  assert.match(typeSource, /export type UsageOverall\s*=/, '缺少 UsageOverall 类型');
  assert.match(apiSource, /usageStats:\s*\(\)[\s\S]*?\/api\/usage\/stats/, '缺少使用统计 API 客户端');
  assert.match(shell, /id:\s*'usage'[\s\S]{0,100}?label:\s*'使用统计'/, '侧栏缺少使用统计入口');
  assert.match(app, /UsagePage/, 'App 未渲染使用统计页面');
  assert.match(app, /api\.usageStats\(\)/, 'App 未请求使用统计');
  assert.match(app, /function refreshStats\(/, 'App 缺少独立统计刷新函数');
  assert.match(app, /statsError/, '使用统计缺少独立错误状态');
  assert.match(usage, /copy\.usageTotalRequests/, '缺少累计请求指标');
  assert.match(usageCopy, /usageTotalRequests:\s*'累计请求'/, '累计请求文案缺失');
  assert.match(usage, /copy\.usageSuccessRate/, '缺少成功率指标');
  assert.match(usageCopy, /usageSuccessRate:\s*'成功率'/, '成功率文案缺失');
  assert.match(usage, /copy\.usageTotalTokens/, '缺少累计 Token 指标');
  assert.match(usageCopy, /usageTotalTokens:\s*'累计 Token'/, '累计 Token 文案缺失');
  assert.match(usage, /copy\.usageUsageKnown/, '缺少用量已返回指标');
  assert.match(usage, /copy\.usageActiveKeys/, '缺少访问 Key 指标');
  assert.match(usage, /copy\.usageRecentSevenDays/, '缺少近 7 天视图');
  assert.match(usageCopy, /usageRecentSevenDays:\s*'近 7 天使用'/, '近 7 天文案缺失');
  assert.match(usage, /copy\.usagePerKey/, '缺少每个 Key 的统计');
  assert.match(usageCopy, /usagePerKey:\s*'每个 Key 的统计'/, '每个 Key 文案缺失');
  assert.match(usage, /copy\.usageUnknown/, 'null Token 未显示未知');
  assert.match(usage, /copy\.usageNeverUsed/, 'null 最近调用未显示从未使用');
  assert.match(usageCopy, /usageNeverUsed:\s*'从未使用'/, '从未使用文案缺失');
  assert.match(usage, /copy\.usageStatsEmptyKeys/, '缺少无 Key 空态');
  assert.match(usageCopy, /usageStatsEmptyKeys:\s*'还没有访问 Key'/, '无 Key 空态文案缺失');
  assert.match(usage, /copy\.usageStatsFailed/, '缺少统计错误提示');
  assert.match(usage, /onGoToKeys/, '无 Key 空态缺少跳转操作');
  assert.match(usage, /<caption[\s\S]*?<\/caption>/, '统计表缺少 caption');
  assert.match(usage, /toLocaleString\(\s*['"]zh-CN['"]\s*\)/, '统计数字/时间未本地化');
  assert.match(usage, /total_tokens/, '页面未展示合计 Token');
});

test('T22 调用记录展示输入/输出 Token 与缓存命中率且未知不猜', () => {
  const typeSource = read(join(srcRoot, 'types.ts'));
  assert.match(
    typeSource,
    /export type Entry\s*=\s*\{[\s\S]*?input_tokens:\s*number\s*\|\s*null;[\s\S]*?output_tokens:\s*number\s*\|\s*null;[\s\S]*?cache_hit_tokens:\s*number\s*\|\s*null;[\s\S]*?cache_miss_tokens:\s*number\s*\|\s*null;[\s\S]*?\};/,
    'Entry 缺少可空的 token/缓存字段',
  );

  const activity = read(join(componentsRoot, 'ActivityTable.tsx'));
  assert.match(activity, /<th>输入 tokens<\/th>/, '调用记录缺少输入 Token 列');
  assert.match(activity, /<th>输出 tokens<\/th>/, '调用记录缺少输出 Token 列');
  assert.match(activity, /<th>缓存命中率<\/th>/, '调用记录缺少缓存命中率列');
  assert.match(activity, /entry\.input_tokens/, '输入 Token 未读取 entry.input_tokens');
  assert.match(activity, /entry\.output_tokens/, '输出 Token 未读取 entry.output_tokens');
  assert.match(activity, /copy\.usageUnknown/, 'Token 或命中率未知时未显示「未知」');
  assert.match(activity, /cache_hit_tokens/, '命中率未使用 cache_hit_tokens');
  assert.match(activity, /cache_miss_tokens/, '命中率未使用 cache_miss_tokens');
  assert.match(
    activity,
    /const hit = entry\.cache_hit_tokens;[\s\S]*?const miss = entry\.cache_miss_tokens;[\s\S]*?hit \+ miss/,
    '命中率分母未使用命中 + 未命中',
  );
  assert.match(activity, /input_tokens/, '命中率缺少 input_tokens 回退');
  assert.match(activity, /Number\.isFinite\(/, '命中率未拦截 NaN/Infinity');
  assert.match(activity, /toFixed\(1\)/, '命中率未格式化为一位小数');
});
