/**
 * Serves apps/site/dist at the real project subpath (/gh-stories/), so tests
 * exercise the same URLs GitHub Pages will. Serving at / would let a missing
 * base path pass here and 404 in production.
 */
import { createServer } from 'node:http';
import { createReadStream, existsSync, statSync } from 'node:fs';
import { extname, join, normalize, resolve } from 'node:path';

const ROOT = resolve(import.meta.dirname, '../../apps/site/dist');
const BASE = '/gh-stories';
const PORT = Number(process.env.PORT ?? 4321);

const TYPES = {
  '.html': 'text/html; charset=utf-8',
  '.css': 'text/css; charset=utf-8',
  '.js': 'text/javascript; charset=utf-8',
  '.json': 'application/json',
  '.svg': 'image/svg+xml',
  '.png': 'image/png',
  '.jpg': 'image/jpeg',
  '.jpeg': 'image/jpeg',
  '.webp': 'image/webp',
  '.mp4': 'video/mp4',
  '.ico': 'image/x-icon',
  '.woff2': 'font/woff2',
  '.txt': 'text/plain; charset=utf-8',
};

createServer((req, res) => {
  const url = new URL(req.url ?? '/', 'http://localhost');
  let path = decodeURIComponent(url.pathname);

  if (!path.startsWith(BASE)) {
    res.writeHead(404, { 'content-type': 'text/plain' });
    res.end(`not found (this server only serves ${BASE}/)`);
    return;
  }
  path = path.slice(BASE.length) || '/';

  // Contain the path: a request must not escape the dist directory.
  let file = join(ROOT, normalize(path).replace(/^(\.\.[/\\])+/, ''));
  if (!file.startsWith(ROOT)) {
    res.writeHead(403).end('forbidden');
    return;
  }
  if (existsSync(file) && statSync(file).isDirectory()) file = join(file, 'index.html');
  if (!existsSync(file)) {
    const notFound = join(ROOT, '404.html');
    res.writeHead(404, { 'content-type': 'text/html; charset=utf-8' });
    if (existsSync(notFound)) createReadStream(notFound).pipe(res);
    else res.end('not found');
    return;
  }
  res.writeHead(200, { 'content-type': TYPES[extname(file)] ?? 'application/octet-stream' });
  createReadStream(file).pipe(res);
}).listen(PORT, () => console.log(`serving ${ROOT} at http://localhost:${PORT}${BASE}/`));
