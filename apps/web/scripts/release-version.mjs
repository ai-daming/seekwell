import { execFileSync } from 'node:child_process';

export const DEV_VERSION_TEXT = '开发版';

const VERSION_TAG_PATTERN =
  /^v(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z.-]+))?(?:\+([0-9A-Za-z.-]+))?$/;
const DEFAULT_API_BASE = 'https://api.github.com';

function runGit(args, cwd) {
  try {
    return execFileSync('git', args, {
      cwd,
      encoding: 'utf8',
      stdio: ['ignore', 'pipe', 'pipe'],
    });
  } catch (error) {
    const detail = String(error.stderr ?? '').trim() || error.message;
    throw new Error(`Git 命令失败（git ${args.join(' ')}）：${detail}`);
  }
}

export function parseVersionTag(tag) {
  const match = VERSION_TAG_PATTERN.exec(tag);
  if (!match) return null;
  return {
    tag,
    core: [Number(match[1]), Number(match[2]), Number(match[3])],
    prerelease: match[4] ? match[4].split('.') : null,
    build: match[5] ?? null,
  };
}

function comparePrerelease(left, right) {
  if (left === null && right === null) return 0;
  if (left === null) return 1;
  if (right === null) return -1;
  const length = Math.max(left.length, right.length);
  for (let index = 0; index < length; index += 1) {
    if (index >= left.length) return -1;
    if (index >= right.length) return 1;
    const a = left[index];
    const b = right[index];
    const aNumeric = /^\d+$/.test(a);
    const bNumeric = /^\d+$/.test(b);
    if (aNumeric && bNumeric) {
      const difference = Number(a) - Number(b);
      if (difference !== 0) return difference < 0 ? -1 : 1;
    } else if (aNumeric !== bNumeric) {
      return aNumeric ? -1 : 1;
    } else if (a !== b) {
      return a < b ? -1 : 1;
    }
  }
  return 0;
}

export function compareVersionTags(left, right) {
  for (let index = 0; index < 3; index += 1) {
    if (left.core[index] !== right.core[index]) {
      return left.core[index] < right.core[index] ? -1 : 1;
    }
  }
  return comparePrerelease(left.prerelease, right.prerelease);
}

export function formatBeijingDate(publishedAt) {
  if (typeof publishedAt !== 'string' || !publishedAt.trim()) {
    throw new Error('Release 缺少 published_at，无法确定发布日期');
  }
  const timestamp = Date.parse(publishedAt);
  if (!Number.isFinite(timestamp)) {
    throw new Error(`Release 的 published_at 不是可解析的 ISO 8601 时间：${publishedAt}`);
  }
  const parts = new Intl.DateTimeFormat('en-US', {
    timeZone: 'Asia/Shanghai',
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
  }).formatToParts(new Date(timestamp));
  const values = Object.fromEntries(parts.map((part) => [part.type, part.value]));
  if (!values.year || !values.month || !values.day) {
    throw new Error(`Release 的 published_at 无法转换为北京时间日期：${publishedAt}`);
  }
  return `${values.year}-${values.month}-${values.day}`;
}

function parseRepositorySlug(url) {
  const patterns = [
    /^https?:\/\/github\.com\/([^/]+)\/([^/#?]+?)(?:\.git)?\/?$/i,
    /^ssh:\/\/git@github\.com\/([^/]+)\/([^/#?]+?)(?:\.git)?\/?$/i,
    /^git@github\.com:([^/]+)\/([^/#?]+?)(?:\.git)?\/?$/i,
  ];
  for (const pattern of patterns) {
    const match = pattern.exec(url.trim());
    if (match) return `${match[1]}/${match[2]}`;
  }
  return null;
}

function resolveRepositorySlug(env, git, remote) {
  const override = String(env.SEEKWELL_REPO ?? '').trim().replace(/\.git$/, '');
  if (override) {
    if (!/^[^/\s]+\/[^/\s]+$/.test(override)) {
      throw new Error('SEEKWELL_REPO 必须是 owner/repo 格式');
    }
    return override;
  }
  let url;
  try {
    url = git(['remote', 'get-url', remote]).trim();
  } catch (error) {
    throw new Error(
      `无法读取远程 ${remote} 的 URL：${error.message}。可在远端存在时设置 SEEKWELL_REPO=owner/repo。`,
    );
  }
  const slug = parseRepositorySlug(url);
  if (!slug) {
    throw new Error(
      `无法从远程 ${remote}（${url}）解析 GitHub 仓库，正式发布只支持 github.com。可设置 SEEKWELL_REPO=owner/repo。`,
    );
  }
  return slug;
}

function assertRemoteTag(git, remote, tag, head) {
  const ref = `refs/tags/${tag}`;
  const output = git(['ls-remote', '--tags', remote, ref, `${ref}^{}`]);
  let direct = null;
  let peeled = null;
  for (const line of output.split(/\r?\n/)) {
    const [sha, name] = line.trim().split(/\s+/);
    if (!sha || !name) continue;
    if (name === `${ref}^{}`) peeled = sha;
    else if (name === ref) direct = sha;
  }
  const remoteSha = peeled ?? direct;
  if (!remoteSha) {
    throw new Error(
      `远程 ${remote} 上没有 tag ${tag}。请先推送 tag（git push ${remote} ${tag}），再执行 git fetch --tags。`,
    );
  }
  if (remoteSha !== head) {
    throw new Error(
      `远程 tag ${tag} 指向 ${remoteSha}，与当前 HEAD ${head} 不一致。请确认 tag 没有被移动，并执行 git fetch --tags 后重试。`,
    );
  }
}

function validateRelease(tag, release) {
  if (!release || typeof release !== 'object') {
    throw new Error('GitHub API 返回格式异常：Release 不是对象');
  }
  if (release.draft === true) {
    throw new Error('Release 仍是 draft，请在 GitHub 上发布后再构建');
  }
  if (typeof release.tag_name !== 'string' || !release.tag_name) {
    throw new Error('Release 缺少 tag_name');
  }
  if (release.tag_name !== tag) {
    throw new Error(`Release 的 tag_name（${release.tag_name}）与请求的 tag（${tag}）不一致`);
  }
  const date = formatBeijingDate(release.published_at);
  return { tag: release.tag_name, text: `${release.tag_name} · ${date}` };
}

export function createReleaseFetcher({ repo, token = '', apiBase = DEFAULT_API_BASE } = {}) {
  return async (tag) => {
    const url = `${apiBase.replace(/\/$/, '')}/repos/${repo}/releases/tags/${encodeURIComponent(tag)}`;
    const headers = {
      Accept: 'application/vnd.github+json',
      'User-Agent': 'seekwell-release-build',
    };
    if (token) headers.Authorization = `Bearer ${token}`;
    let response;
    try {
      response = await fetch(url, { headers, signal: AbortSignal.timeout(15000) });
    } catch (error) {
      throw new Error(
        `请求 GitHub Release 失败（tag ${tag}）：${error.message}。请检查网络，失败时不要重试为开发版。`,
      );
    }
    if (!response.ok) {
      let detail = '';
      try {
        const body = await response.json();
        if (body && typeof body.message === 'string') detail = `：${body.message}`;
      } catch {
        // 忽略非 JSON 错误响应，状态码本身已足够定位问题。
      }
      let hint = '';
      if (response.status === 404) {
        hint = '该 tag 可能还没有创建 Release，或 Release 是 draft。';
      } else if (response.status === 401 || response.status === 403) {
        hint = '请检查 GITHUB_TOKEN/GH_TOKEN 或 GitHub API 速率限制。';
      }
      throw new Error(`GitHub API 返回 ${response.status}（tag ${tag}）${detail}${hint}`);
    }
    try {
      return await response.json();
    } catch (error) {
      throw new Error(`GitHub API 返回的 Release 内容无法解析：${error.message}`);
    }
  };
}

export async function resolveReleaseVersionText({ env = process.env, git, fetchRelease } = {}) {
  const run = git ?? ((args) => runGit(args, process.cwd()));
  try {
    run(['rev-parse', '--show-toplevel']);
  } catch (error) {
    throw new Error(`正式发布构建需要 Git 工作树：${error.message}`);
  }

  const head = String(run(['rev-parse', 'HEAD'])).trim();
  if (!/^[0-9a-f]{40}$/i.test(head)) {
    throw new Error(`无法取得当前 HEAD 提交 SHA：${head || '空值'}`);
  }

  const status = String(run(['status', '--porcelain'])).trim();
  if (status) {
    const changed = status.split(/\r?\n/).slice(0, 5).join('\n');
    throw new Error(`正式发布构建要求工作区干净，当前有未提交改动：\n${changed}`);
  }

  const tagNames = String(run(['tag', '--points-at', 'HEAD']))
    .split(/\r?\n/)
    .map((tag) => tag.trim())
    .filter(Boolean);
  if (tagNames.length === 0) {
    throw new Error(
      `当前 HEAD ${head} 没有 tag。请先在当前提交上创建 Release 和 tag，再执行 git fetch --tags；正式构建不会使用其他提交或 GitHub 上的最新 Release。`,
    );
  }
  const candidates = tagNames.map(parseVersionTag).filter(Boolean);
  if (candidates.length === 0) {
    throw new Error(
      `当前 HEAD 的 tag（${tagNames.join(', ')}）都不是可识别版本 tag。请使用 vX.Y.Z 或带预发布后缀的形式。`,
    );
  }

  const remote = String(env.SEEKWELL_RELEASE_REMOTE ?? '').trim() || 'origin';
  const repo = resolveRepositorySlug(env, run, remote);
  const token = String(env.GITHUB_TOKEN ?? env.GH_TOKEN ?? '').trim();
  const apiBase = String(env.SEEKWELL_GITHUB_API ?? '').trim() || DEFAULT_API_BASE;
  const getRelease = fetchRelease ?? createReleaseFetcher({ repo, token, apiBase });

  const valid = [];
  const failures = [];
  for (const candidate of candidates) {
    assertRemoteTag(run, remote, candidate.tag, head);
    try {
      const release = await getRelease(candidate.tag);
      valid.push(validateRelease(candidate.tag, release));
    } catch (error) {
      failures.push(`${candidate.tag}：${error.message}`);
    }
  }

  if (valid.length === 0) {
    throw new Error(
      `没有找到指向当前提交 ${head} 的有效 Release：\n- ${failures.join('\n- ')}\n请确认 tag 已推送、Release 已创建且不是 draft。`,
    );
  }

  const ranked = valid
    .map((item) => ({ ...item, parsed: parseVersionTag(item.tag) }))
    .sort((left, right) => compareVersionTags(right.parsed, left.parsed));
  if (ranked.length > 1 && compareVersionTags(ranked[0].parsed, ranked[1].parsed) === 0) {
    throw new Error(
      `同一提交上有多个无法确定唯一最高版本的 Release：${ranked.map((item) => item.tag).join(', ')}。请先处理重复 tag/Release。`,
    );
  }
  return ranked[0].text;
}

export async function resolveVersionText({
  release = false,
  env = process.env,
  cwd = process.cwd(),
  git,
  fetchRelease,
} = {}) {
  if (!release) return DEV_VERSION_TEXT;
  return resolveReleaseVersionText({
    env,
    git: git ?? ((args) => runGit(args, cwd)),
    fetchRelease,
  });
}
