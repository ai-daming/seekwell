import { test } from 'node:test';
import assert from 'node:assert/strict';
import { execFileSync, spawn } from 'node:child_process';
import {
  cpSync,
  existsSync,
  mkdtempSync,
  readFileSync,
  readdirSync,
  rmSync,
  statSync,
  symlinkSync,
  writeFileSync,
} from 'node:fs';
import { createServer } from 'node:http';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

import {
  DEV_VERSION_TEXT,
  formatBeijingDate,
  resolveVersionText,
} from '../scripts/release-version.mjs';

const webRoot = join(dirname(fileURLToPath(import.meta.url)), '..');
const HEAD = '158015429f76e6d775e2c7e03d91392c82d8356e';
const OTHER = 'bf2aec9ba59b8c7c395a9f26f34f1531bbb5825b';

function read(path) {
  return readFileSync(path, 'utf8');
}

function walk(dir) {
  return readdirSync(dir).flatMap((name) => {
    const path = join(dir, name);
    return statSync(path).isDirectory() ? walk(path) : [path];
  });
}

function createGit(options = {}) {
  const {
    head = HEAD,
    tags = [],
    status = '',
    remoteUrl = 'https://github.com/ai-daming/seekwell.git',
    remoteTags = {},
  } = options;
  const calls = [];
  const git = (args) => {
    calls.push(args.join(' '));
    const [command, ...rest] = args;
    if (command === 'rev-parse' && rest[0] === '--show-toplevel') return '/repo\n';
    if (command === 'rev-parse' && rest[0] === 'HEAD') return `${head}\n`;
    if (command === 'status' && rest[0] === '--porcelain') return status;
    if (command === 'tag' && rest[0] === '--points-at') {
      return tags.map((tag) => `${tag}\n`).join('');
    }
    if (command === 'remote' && rest[0] === 'get-url') return `${remoteUrl}\n`;
    if (command === 'ls-remote') {
      const ref = rest[rest.length - 1];
      const tag = ref.replace(/^refs\/tags\//, '').replace(/\^\{\}$/, '');
      const entry = remoteTags[tag];
      if (entry === undefined) return '';
      if (Array.isArray(entry)) return entry.map((line) => `${line}\n`).join('');
      return `${entry}\trefs/tags/${tag}\n`;
    }
    throw new Error(`unexpected git call: ${args.join(' ')}`);
  };
  git.calls = calls;
  return git;
}

function release(tag, publishedAt, extra = {}) {
  return {
    tag_name: tag,
    draft: false,
    prerelease: false,
    published_at: publishedAt,
    ...extra,
  };
}

function createFetch(releases) {
  const calls = [];
  const fetchRelease = async (tag) => {
    calls.push(tag);
    const value = releases[tag];
    if (value instanceof Error) throw value;
    if (value === undefined) {
      throw Object.assign(new Error(`GitHub API 返回 404：tag ${tag} 没有对应的 Release`), {
        status: 404,
      });
    }
    return value;
  };
  fetchRelease.calls = calls;
  return fetchRelease;
}

function rejectsWith(promise, pattern) {
  return assert.rejects(promise, (error) => {
    assert.match(String(error.message), pattern);
    return true;
  });
}

test('开发版不读取 Git 或 GitHub Release', async () => {
  const result = await resolveVersionText({
    release: false,
    git: () => {
      throw new Error('不应调用 git');
    },
    fetchRelease: () => {
      throw new Error('不应请求 GitHub');
    },
    env: {},
  });
  assert.equal(result, DEV_VERSION_TEXT);
  assert.equal(result, '开发版');
});

test('当前提交有精确 tag 和有效 Release 时输出 tag 与北京时间日期', async () => {
  const git = createGit({ tags: ['v0.4.0'], remoteTags: { 'v0.4.0': HEAD } });
  const fetchRelease = createFetch({
    'v0.4.0': release('v0.4.0', '2026-11-13T16:29:56Z'),
  });
  const text = await resolveVersionText({ release: true, git, fetchRelease, env: {} });
  assert.equal(text, 'v0.4.0 · 2026-11-14');
  assert.match(text, /^v0\.4\.0 · 2026-11-14$/);
  assert.deepEqual(fetchRelease.calls, ['v0.4.0']);
});

test('注解 tag 通过 ^{} 剥开后的提交匹配 HEAD', async () => {
  const tagObject = '89a788a000000000000000000000000000000000';
  const git = createGit({
    tags: ['v0.1.0'],
    remoteTags: {
      'v0.1.0': [
        `${tagObject}\trefs/tags/v0.1.0`,
        `${HEAD}\trefs/tags/v0.1.0^{}`,
      ],
    },
  });
  const fetchRelease = createFetch({
    'v0.1.0': release('v0.1.0', '2026-11-12T17:03:50Z'),
  });
  const text = await resolveVersionText({ release: true, git, fetchRelease, env: {} });
  assert.equal(text, 'v0.1.0 · 2026-11-13');
});

test('轻量 tag 直接使用远程行匹配 HEAD', async () => {
  const git = createGit({ tags: ['v0.3.0'], remoteTags: { 'v0.3.0': HEAD } });
  const fetchRelease = createFetch({
    'v0.3.0': release('v0.3.0', '2026-11-13T11:03:28Z'),
  });
  const text = await resolveVersionText({ release: true, git, fetchRelease, env: {} });
  assert.equal(text, 'v0.3.0 · 2026-11-13');
});

test('HEAD 没有 tag 时失败并提示 fetch tags', async () => {
  const git = createGit({ tags: [] });
  await rejectsWith(
    resolveVersionText({ release: true, git, fetchRelease: createFetch({}), env: {} }),
    /tag[\s\S]*fetch --tags|fetch --tags[\s\S]*tag/i,
  );
});

test('tag 没有对应 Release 时失败', async () => {
  const git = createGit({ tags: ['v0.5.0'], remoteTags: { 'v0.5.0': HEAD } });
  await rejectsWith(
    resolveVersionText({ release: true, git, fetchRelease: createFetch({}), env: {} }),
    /404[\s\S]*Release|Release[\s\S]*404/,
  );
});

test('Release 是 draft 时失败', async () => {
  const git = createGit({ tags: ['v0.5.0'], remoteTags: { 'v0.5.0': HEAD } });
  const fetchRelease = createFetch({
    'v0.5.0': release('v0.5.0', '2026-11-13T16:29:56Z', { draft: true }),
  });
  await rejectsWith(
    resolveVersionText({ release: true, git, fetchRelease, env: {} }),
    /draft/i,
  );
});

test('published_at 缺失或非法时失败', async () => {
  for (const publishedAt of [null, '', 'not-a-date', '2026-13-45T99:99:99Z']) {
    const git = createGit({ tags: ['v0.5.0'], remoteTags: { 'v0.5.0': HEAD } });
    const fetchRelease = createFetch({
      'v0.5.0': release('v0.5.0', publishedAt),
    });
    await rejectsWith(
      resolveVersionText({ release: true, git, fetchRelease, env: {} }),
      /published_at/,
    );
  }
});

test('GitHub API 404/403/500 与网络异常都失败并保留原因', async () => {
  const cases = [
    [404, 'HTTP 404 Not Found'],
    [403, 'HTTP 403 rate limit exceeded'],
    [500, 'HTTP 500 Internal Server Error'],
    [null, 'fetch failed: getaddrinfo ENOTFOUND api.github.com'],
  ];
  for (const [status, message] of cases) {
    const git = createGit({ tags: ['v0.5.0'], remoteTags: { 'v0.5.0': HEAD } });
    const error = Object.assign(new Error(message), status ? { status } : {});
    const fetchRelease = createFetch({ 'v0.5.0': error });
    await rejectsWith(
      resolveVersionText({ release: true, git, fetchRelease, env: {} }),
      new RegExp(message.split(':')[0].replace(/[.*+?^${}()|[\]\\]/g, '\\$&')),
    );
  }
});

test('最新 Release 是 v0.4.0 但当前提交是另一个提交时绝不返回 v0.4.0', async () => {
  const git = createGit({ head: OTHER, tags: ['v0.5.0'], remoteTags: { 'v0.5.0': OTHER } });
  const fetchRelease = createFetch({
    'v0.5.0': release('v0.4.0', '2026-11-13T16:29:56Z'),
  });
  await rejectsWith(
    resolveVersionText({ release: true, git, fetchRelease, env: {} }),
    /tag_name[\s\S]*v0\.5\.0|v0\.5\.0[\s\S]*tag_name|不一致/,
  );
});

test('多个 tag 指向同一 HEAD 时选择最高版本', async () => {
  const git = createGit({
    tags: ['v0.4.0', 'v0.5.0-rc.1'],
    remoteTags: { 'v0.4.0': HEAD, 'v0.5.0-rc.1': HEAD },
  });
  const fetchRelease = createFetch({
    'v0.4.0': release('v0.4.0', '2026-11-13T16:29:56Z'),
    'v0.5.0-rc.1': release('v0.5.0-rc.1', '2026-11-14T16:00:00Z'),
  });
  const text = await resolveVersionText({ release: true, git, fetchRelease, env: {} });
  assert.equal(text, 'v0.5.0-rc.1 · 2026-11-15');
});

test('多个 tag 版本无法唯一排序时失败', async () => {
  const tags = ['v0.5.0+build.1', 'v0.5.0+build.2'];
  const git = createGit({
    tags,
    remoteTags: Object.fromEntries(tags.map((tag) => [tag, HEAD])),
  });
  const fetchRelease = createFetch(
    Object.fromEntries(tags.map((tag) => [tag, release(tag, '2026-11-13T16:29:56Z')])),
  );
  await rejectsWith(
    resolveVersionText({ release: true, git, fetchRelease, env: {} }),
    /无法确定|歧义|ambiguous/i,
  );
});

test('远程 tag 指向的提交不是 HEAD 时失败', async () => {
  const git = createGit({ tags: ['v0.5.0'], remoteTags: { 'v0.5.0': OTHER } });
  await rejectsWith(
    resolveVersionText({ release: true, git, fetchRelease: createFetch({}), env: {} }),
    /远程|remote[\s\S]*HEAD|HEAD[\s\S]*远程/i,
  );
});

test('工作区有未提交改动时失败', async () => {
  const git = createGit({
    tags: ['v0.5.0'],
    remoteTags: { 'v0.5.0': HEAD },
    status: ' M apps/web/src/components/Shell.tsx\n',
  });
  await rejectsWith(
    resolveVersionText({ release: true, git, fetchRelease: createFetch({}), env: {} }),
    /未提交|工作区不干净/,
  );
});

test('日期固定按北京时间转换并覆盖 UTC 16:00 边界', () => {
  assert.equal(formatBeijingDate('2026-11-13T16:29:56Z'), '2026-11-14');
  assert.equal(formatBeijingDate('2026-11-13T15:59:59Z'), '2026-11-13');
  assert.equal(formatBeijingDate('2026-11-13T16:00:00Z'), '2026-11-14');
  assert.equal(formatBeijingDate('2026-11-14T00:00:00+08:00'), '2026-11-14');
  assert.throws(() => formatBeijingDate('not-a-date'), /published_at|日期|时间/);
});

test('构建机 TZ 不影响北京时间日期', () => {
  const original = process.env.TZ;
  try {
    for (const zone of ['UTC', 'America/Los_Angeles', 'Asia/Tokyo']) {
      process.env.TZ = zone;
      assert.equal(formatBeijingDate('2026-11-13T16:29:56Z'), '2026-11-14', zone);
      assert.equal(formatBeijingDate('2026-11-13T15:59:59Z'), '2026-11-13', zone);
    }
  } finally {
    if (original === undefined) delete process.env.TZ;
    else process.env.TZ = original;
  }
});

test('版本文字严格使用空格、中点和 YYYY-MM-DD', async () => {
  const git = createGit({ tags: ['v0.5.0'], remoteTags: { 'v0.5.0': HEAD } });
  const fetchRelease = createFetch({
    'v0.5.0': release('v0.5.0', '2026-11-13T16:29:56Z'),
  });
  const text = await resolveVersionText({ release: true, git, fetchRelease, env: {} });
  assert.equal(text, 'v0.5.0 · 2026-11-14');
  assert.match(text, /^v0\.5\.0 \u00b7 \d{4}-\d{2}-\d{2}$/);
  assert.ok(!/v0\.5\.0\s{2,}/.test(text), '版本文字不应有多余空格');
  assert.ok(!text.includes(' - ') && !text.includes(' | '), '版本文字不应使用其他分隔符');
});

function buildToTemp(env) {
  const outDir = mkdtempSync(join(tmpdir(), 'seekwell-version-'));
  const viteBin = join(webRoot, 'node_modules', 'vite', 'bin', 'vite.js');
  const childEnv = { ...process.env, ...env };
  if (!env.SEEKWELL_VERSION_TEXT) delete childEnv.SEEKWELL_VERSION_TEXT;
  try {
    execFileSync(process.execPath, [viteBin, 'build', '--outDir', outDir, '--emptyOutDir'], {
      cwd: webRoot,
      env: childEnv,
      stdio: 'pipe',
    });
    return walk(outDir)
      .filter((file) => file.endsWith('.js'))
      .map(read)
      .join('\n');
  } finally {
    rmSync(outDir, { recursive: true, force: true });
  }
}

test('普通构建产物显示开发版且不含正式版本文字', () => {
  const bundle = buildToTemp({});
  assert.ok(bundle.includes('开发版'), '普通构建产物缺少「开发版」');
  assert.doesNotMatch(bundle, /v\d+\.\d+\.\d+ · \d{4}-\d{2}-\d{2}/, '普通构建产物不应含正式版本文字');
});

test('正式构建注入的版本文字原样进入产物', () => {
  const text = 'v0.5.0 · 2026-11-14';
  const bundle = buildToTemp({ SEEKWELL_VERSION_TEXT: text });
  assert.ok(bundle.includes(text), `构建产物缺少版本文字 ${text}`);
  assert.ok(!bundle.includes('开发版'), '正式构建产物不应显示开发版');
});

function runCommand(command, args, options) {
  return new Promise((resolve, reject) => {
    const child = spawn(command, args, options);
    let output = '';
    child.stdout.on('data', (chunk) => {
      output += chunk;
    });
    child.stderr.on('data', (chunk) => {
      output += chunk;
    });
    child.on('error', reject);
    child.on('close', (code) => resolve({ code, output }));
  });
}

test('build:release 用本地 fixture Release 构建，失败时不残留 dist', async () => {
  const repo = mkdtempSync(join(tmpdir(), 'seekwell-release-repo-'));
  const remote = mkdtempSync(join(tmpdir(), 'seekwell-release-remote-'));
  const web = join(repo, 'web');
  let mode = 'ok';
  const server = createServer((request, response) => {
    response.setHeader('content-type', 'application/json');
    if (mode === 'ok') {
      response.end(
        JSON.stringify({
          tag_name: 'v0.5.0',
          draft: false,
          prerelease: false,
          published_at: '2026-11-13T16:29:56Z',
        }),
      );
    } else {
      response.statusCode = 404;
      response.end(JSON.stringify({ message: 'Not Found' }));
    }
  });
  try {
    await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
    const apiBase = `http://127.0.0.1:${server.address().port}`;

    cpSync(join(webRoot, 'src'), join(web, 'src'), { recursive: true });
    cpSync(join(webRoot, 'scripts'), join(web, 'scripts'), { recursive: true });
    cpSync(join(webRoot, 'public'), join(web, 'public'), { recursive: true });
    for (const file of ['index.html', 'package.json', 'tsconfig.json', 'vite.config.ts']) {
      cpSync(join(webRoot, file), join(web, file));
    }
    writeFileSync(join(web, '.gitignore'), 'node_modules\ndist\n');
    symlinkSync(join(webRoot, 'node_modules'), join(web, 'node_modules'), 'dir');

    execFileSync('git', ['init', '-q'], { cwd: repo });
    execFileSync('git', ['config', 'user.email', 'fixture@example.com'], { cwd: repo });
    execFileSync('git', ['config', 'user.name', 'Fixture'], { cwd: repo });
    execFileSync('git', ['add', '.'], { cwd: repo });
    execFileSync('git', ['commit', '-q', '-m', 'fixture release'], { cwd: repo });
    execFileSync('git', ['tag', 'v0.5.0'], { cwd: repo });
    execFileSync('git', ['init', '-q', '--bare'], { cwd: remote });
    execFileSync('git', ['remote', 'add', 'fixture', remote], { cwd: repo });
    execFileSync('git', ['push', '-q', 'fixture', 'refs/tags/v0.5.0'], { cwd: repo });

    const env = {
      ...process.env,
      SEEKWELL_REPO: 'fixture/repo',
      SEEKWELL_RELEASE_REMOTE: 'fixture',
      SEEKWELL_GITHUB_API: apiBase,
      GITHUB_TOKEN: '',
      GH_TOKEN: '',
    };
    const npm = process.platform === 'win32' ? 'npm.cmd' : 'npm';
    const success = await runCommand(npm, ['run', 'build:release'], { cwd: web, env });
    assert.equal(success.code, 0, success.output);
    const bundle = walk(join(web, 'dist'))
      .filter((file) => file.endsWith('.js'))
      .map(read)
      .join('\n');
    assert.ok(bundle.includes('v0.5.0 · 2026-11-14'), 'fixture Release 未写入构建产物');

    mode = 'fail';
    const failed = await runCommand(npm, ['run', 'build:release'], { cwd: web, env });
    assert.notEqual(failed.code, 0, '没有对应 Release 时正式构建必须失败');
    assert.ok(!existsSync(join(web, 'dist')), '正式构建失败后不应残留 dist');
  } finally {
    await new Promise((resolve) => server.close(resolve));
    rmSync(repo, { recursive: true, force: true });
    rmSync(remote, { recursive: true, force: true });
  }
});

test('界面源码使用构建期版本常量且不再写死 V0.1', () => {
  const shell = read(join(webRoot, 'src', 'components', 'Shell.tsx'));
  assert.doesNotMatch(shell, /V0\.1/, 'Shell.tsx 仍写死 V0.1');
  assert.match(shell, /__SEEKWELL_VERSION__/, 'Shell.tsx 未使用构建期版本常量');
  assert.match(shell, /className="version"[\s\S]*?SEEKWELL[\s\S]*?__SEEKWELL_VERSION__/, '.version 区域未渲染注入版本');

  const viteConfig = read(join(webRoot, 'vite.config.ts'));
  assert.match(viteConfig, /__SEEKWELL_VERSION__/, 'vite.config.ts 未定义版本常量');
  assert.match(viteConfig, /define\s*:/, 'vite.config.ts 未使用 define 注入');

  const pkg = JSON.parse(read(join(webRoot, 'package.json')));
  assert.match(pkg.scripts['build:release'], /build-release/, '缺少 build:release 脚本');
  assert.doesNotMatch(pkg.scripts.build, /build-release/, '普通 build 不应走正式发布流程');
});
