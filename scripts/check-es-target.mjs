// Verifies every emitted JS chunk parses as ES2019 (Chrome 79 / webOS 6) and
// that the CSS avoids features Chrome 79 lacks.
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { join } from 'node:path';
import { createRequire } from 'node:module';

const require = createRequire(join(process.cwd(), 'package.json'));
const acorn = require('acorn');

const dir = process.argv[2] || 'dist';
const files = [];
(function walk(d) {
  for (const f of readdirSync(d)) {
    const p = join(d, f);
    if (statSync(p).isDirectory()) walk(p);
    else files.push(p);
  }
})(dir);

let failed = false;
for (const f of files) {
  if (f.endsWith('.js')) {
    try {
      acorn.parse(readFileSync(f, 'utf8'), { ecmaVersion: 2019, sourceType: 'module' });
    } catch (e) {
      failed = true;
      console.error(`ES2019 check failed: ${f}: ${e.message}`);
    }
  }
  if (f.endsWith('.css')) {
    const css = readFileSync(f, 'utf8');
    const banned = [
      [/aspect-ratio\s*:/, 'aspect-ratio'],
      [/[{;]\s*inset\s*:/, 'inset'],
      [/:focus-visible/, ':focus-visible'],
      [/:is\(/, ':is()'],
      [/:where\(/, ':where()'],
    ];
    for (const [re, name] of banned) {
      if (re.test(css)) {
        failed = true;
        console.error(`CSS check failed: ${f} uses ${name} (unsupported in Chrome 79)`);
      }
    }
    // flex gap: only allowed in grid rules (grid-*-gap / gap inside display:grid blocks is fine in Chrome 66+).
    for (const block of css.split('}')) {
      if (/(^|[;{\s])gap\s*:/.test(block) && !/display\s*:\s*grid/.test(block)) {
        failed = true;
        console.error(`CSS check failed: ${f} uses gap outside a grid rule: ${block.trim().slice(0, 80)}`);
      }
    }
  }
}
if (failed) process.exit(1);
console.log(`ES2019/CSS target check passed (${files.filter((f) => f.endsWith('.js')).length} JS, ${files.filter((f) => f.endsWith('.css')).length} CSS files)`);
