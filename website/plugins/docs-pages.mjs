// Remark plugin that lets the Markdown in docs/ be read on GitHub and on the
// site from the same files. On GitHub a page starts with its own H1 and links
// to other pages by file name; on the site the title comes from the front
// matter and a page lives at a route, so the plugin drops the leading H1 and
// rewrites each relative link: to a page of docs/ it becomes that page's
// route, to anything else in the repository a link to the file on GitHub.
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const docsRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../docs');
const repository = 'https://github.com/azverinus/flagnetic';

function visit(node, fn) {
  fn(node);
  for (const child of node.children ?? []) visit(child, fn);
}

// route is the URL path of the page at rel, a path inside docs/.
export function route(rel, base) {
  const slug = rel
    .replace(/\.md$/, '')
    .replace(/(^|\/)README$/i, '')
    .replace(/\/$/, '')
    .toLowerCase();
  return `${base}${slug ? `/${slug}` : ''}/`;
}

export default function docsPages({ base }) {
  return () => (tree, file) => {
    const from = file.path ? path.resolve(file.path) : '';
    if (!from.startsWith(docsRoot + path.sep)) return;

    const first = tree.children.findIndex((n) => n.type !== 'html');
    if (first >= 0 && tree.children[first].type === 'heading' && tree.children[first].depth === 1) {
      tree.children.splice(first, 1);
    }

    visit(tree, (node) => {
      if (node.type !== 'link' || !node.url) return;
      if (/^([a-z][a-z0-9+.-]*:|#|\/\/)/i.test(node.url)) return;
      const hashAt = node.url.indexOf('#');
      const target = hashAt < 0 ? node.url : node.url.slice(0, hashAt);
      const hash = hashAt < 0 ? '' : node.url.slice(hashAt);
      const resolved = path.resolve(path.dirname(from), decodeURI(target));
      const rel = path.relative(docsRoot, resolved).split(path.sep).join('/');
      if (!rel.startsWith('..') && rel.endsWith('.md')) {
        node.url = route(rel, base) + hash;
        return;
      }
      const repoRel = path.relative(path.resolve(docsRoot, '..'), resolved).split(path.sep).join('/');
      const kind = /\.[a-z0-9]+$/i.test(repoRel) ? 'blob' : 'tree';
      node.url = `${repository}/${kind}/main/${repoRel}${hash}`;
    });
  };
}
