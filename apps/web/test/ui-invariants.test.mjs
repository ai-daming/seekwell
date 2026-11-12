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

test('T3 外部接口集合恰好为五条', () => {
  const paths = new Set();
  for (const match of allSource.matchAll(/['"`](\/(?:api|v1)\/[^'"`]*)['"`]/g)) {
    paths.add(match[1].replace(/\$\{[^}]+\}/g, '${id}'));
  }
  assert.deepEqual([...paths].sort(), [
    '/api/channels',
    '/api/channels/${id}',
    '/api/channels/${id}/discover',
    '/api/requests',
    '/v1/tools/search',
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
