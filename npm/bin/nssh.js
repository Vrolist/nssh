#!/usr/bin/env node
'use strict';

const { spawnSync } = require('child_process');
const path = require('path');

// 平台分包映射：运行平台 -> 平台包名（与 optionalDependencies 一致）
const MAP = {
  'darwin-arm64': 'nssh-darwin-arm64',
  'darwin-x64': 'nssh-darwin-x64',
  'win32-x64': 'nssh-win32-x64',
  'linux-x64': 'nssh-linux-x64',
  'linux-arm64': 'nssh-linux-arm64',
};

const key = `${process.platform}-${process.arch}`;
const pkg = MAP[key];

if (!pkg) {
  console.error(`[nssh] 不支持当前平台: ${process.platform}-${process.arch}`);
  console.error('[nssh] 支持的平台: darwin-arm64, darwin-x64, win32-x64, linux-x64, linux-arm64');
  process.exit(1);
}

// 定位平台包内的二进制（npm 把 optionalDependencies 装进 node_modules）
let binary;
try {
  const pkgDir = path.dirname(require.resolve(`@buladou/${pkg}/package.json`));
  binary = path.join(pkgDir, 'bin', process.platform === 'win32' ? 'nssh.exe' : 'nssh');
} catch (err) {
  console.error(`[nssh] 缺少平台包 @buladou/${pkg}，请完整安装：npm install -g @buladou/nssh`);
  process.exit(1);
}

const result = spawnSync(binary, process.argv.slice(2), { stdio: 'inherit' });
process.exit(result.status === null ? 1 : result.status);
