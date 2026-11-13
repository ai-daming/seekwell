import { spawnSync } from 'node:child_process';
import { rmSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

import { resolveVersionText } from './release-version.mjs';

const webRoot = dirname(dirname(fileURLToPath(import.meta.url)));
const distRoot = join(webRoot, 'dist');
const viteBin = join(webRoot, 'node_modules', 'vite', 'bin', 'vite.js');
const checkOnly = process.argv.includes('--check');

async function main() {
  const versionText = await resolveVersionText({
    release: true,
    env: process.env,
    cwd: webRoot,
  });
  if (checkOnly) {
    console.log(`正式发布版本校验通过：${versionText}`);
    return 0;
  }

  // 清掉旧产物，避免正式构建失败后留下可被误认为本次发布的 dist。
  rmSync(distRoot, { recursive: true, force: true });
  console.log(`正式发布构建版本：${versionText}`);

  const result = spawnSync(process.execPath, [viteBin, 'build'], {
    cwd: webRoot,
    env: { ...process.env, SEEKWELL_VERSION_TEXT: versionText },
    stdio: 'inherit',
  });
  if (result.error) throw result.error;
  return result.status ?? 1;
}

main()
  .then((code) => {
    process.exitCode = code;
  })
  .catch((error) => {
    if (!checkOnly) rmSync(distRoot, { recursive: true, force: true });
    console.error(`\n正式发布构建失败：${error.message}\n`);
    process.exitCode = 1;
  });
