// Checks the links of the built site: every internal link of every page must
// reach a page or file of dist/, and a link with a fragment must reach an
// element with that id. Run after `astro build`.
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const dist = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../dist');
const base = '/flagnetic';

const pages = [];
(function walk(dir) {
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const p = path.join(dir, entry.name);
    if (entry.isDirectory()) walk(p);
    else if (entry.name.endsWith('.html')) pages.push(p);
  }
})(dist);

const ids = new Map();
function idsOf(file) {
  if (!ids.has(file)) {
    const html = fs.readFileSync(file, 'utf8');
    ids.set(file, new Set([...html.matchAll(/\sid="([^"]*)"/g)].map((m) => m[1])));
  }
  return ids.get(file);
}

function target(pathname) {
  const rel = decodeURIComponent(pathname.slice(base.length)).replace(/^\//, '');
  for (const candidate of [rel, path.join(rel, 'index.html'), `${rel}.html`]) {
    const file = path.join(dist, candidate);
    if (fs.existsSync(file) && fs.statSync(file).isFile()) return file;
  }
  return null;
}

const failures = [];
let checked = 0;
for (const page of pages) {
  const html = fs.readFileSync(page, 'utf8');
  const here = '/' + path.relative(dist, page).replace(/index\.html$/, '');
  for (const m of html.matchAll(/<a\s[^>]*?href="([^"]*)"/g)) {
    const href = m[1].replace(/&amp;/g, '&');
    if (/^([a-z][a-z0-9+.-]*:|\/\/)/i.test(href)) continue;
    const url = new URL(href, `http://site${base}${here}`);
    if (!url.pathname.startsWith(`${base}/`) && url.pathname !== base) {
      failures.push(`${here}: ${href} is outside ${base}/`);
      continue;
    }
    checked++;
    const file = target(url.pathname);
    if (!file) {
      failures.push(`${here}: ${href} reaches no page`);
    } else if (url.hash.length > 1 && file.endsWith('.html') && !idsOf(file).has(decodeURIComponent(url.hash.slice(1)))) {
      failures.push(`${here}: ${href} reaches no element ${url.hash}`);
    }
  }
}

if (failures.length > 0) {
  console.error(`${failures.length} broken links:\n${[...new Set(failures)].join('\n')}`);
  process.exit(1);
}
console.log(`${checked} links in ${pages.length} pages resolve`);
