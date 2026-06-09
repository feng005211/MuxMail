import { cpSync, existsSync, mkdirSync, rmSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import process from 'node:process';
import { fileURLToPath, pathToFileURL } from 'node:url';

const scriptDir = dirname(fileURLToPath(import.meta.url));
const repoRoot = resolve(scriptDir, '..', '..', '..');
const source = resolve(repoRoot, 'web', 'admin', 'dist');
const target = resolve(repoRoot, 'internal', 'api', 'admin_dist');

export function syncAdminDist() {
  if (!existsSync(resolve(source, 'index.html'))) {
    throw new Error('web/admin/dist/index.html is required; run npm run build first');
  }

  rmSync(target, { recursive: true, force: true });
  mkdirSync(target, { recursive: true });
  cpSync(source, target, { recursive: true });
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  syncAdminDist();
}
