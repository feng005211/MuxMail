import { mkdirSync, rmSync, writeFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import process from 'node:process';
import { fileURLToPath, pathToFileURL } from 'node:url';

const scriptDir = dirname(fileURLToPath(import.meta.url));
const repoRoot = resolve(scriptDir, '..', '..', '..');
const target = resolve(repoRoot, 'internal', 'api', 'admin_dist');

const placeholderIndex = `<!doctype html>
<html lang="en">
  <head>
    <meta charset="UTF-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <title>MuxMail Admin</title>
  </head>
  <body>
    <main>
      <h1>MuxMail Admin</h1>
      <p>The admin UI has not been built into this binary.</p>
    </main>
  </body>
</html>
`;

const placeholderAsset = 'MuxMail Admin embedded asset placeholder.\n';

export function restoreAdminPlaceholder() {
  rmSync(target, { recursive: true, force: true });
  mkdirSync(resolve(target, 'assets'), { recursive: true });
  writeFileSync(resolve(target, 'index.html'), placeholderIndex);
  writeFileSync(resolve(target, 'assets', 'admin-placeholder.txt'), placeholderAsset);
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  restoreAdminPlaceholder();
}
