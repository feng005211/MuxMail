import { spawnSync } from 'node:child_process';
import { mkdirSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import process from 'node:process';
import { fileURLToPath } from 'node:url';

import { restoreAdminPlaceholder } from './restore-admin-placeholder.mjs';
import { syncAdminDist } from './sync-admin-dist.mjs';

const scriptDir = dirname(fileURLToPath(import.meta.url));
const repoRoot = resolve(scriptDir, '..', '..', '..');
const adminRoot = resolve(repoRoot, 'web', 'admin');
const binDir = resolve(repoRoot, 'bin');

function run(command, args, cwd) {
  const commandLine = [command, ...args].join(' ');
  const result = spawnSync(process.platform === 'win32' ? commandLine : command, process.platform === 'win32' ? [] : args, {
    cwd,
    shell: process.platform === 'win32',
    stdio: 'inherit',
  });
  if (result.error) {
    const error = new Error(`${command} ${args.join(' ')} failed: ${result.error.message}`);
    error.exitCode = 1;
    throw error;
  }
  if (result.status !== 0) {
    const code = typeof result.status === 'number' ? result.status : 1;
    const error = new Error(`${command} ${args.join(' ')} failed with exit code ${code}`);
    error.exitCode = code;
    throw error;
  }
}

try {
  run('npm', ['ci'], adminRoot);
  run('npm', ['run', 'build'], adminRoot);
  syncAdminDist();
  mkdirSync(binDir, { recursive: true });
  run('go', ['build', '-o', './bin/muxmail', './cmd/muxmail'], repoRoot);
} catch (error) {
  console.error(error.message);
  process.exitCode = error.exitCode || 1;
} finally {
  try {
    restoreAdminPlaceholder();
  } catch (error) {
    console.error(`restore admin placeholder failed: ${error.message}`);
    process.exitCode = process.exitCode || 1;
  }
}
