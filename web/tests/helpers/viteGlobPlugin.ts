/**
 * Bun has no `import.meta.glob`; Vite expands it at build time. This test preload expands the
 * eager, default-import form used by src/i18n into static imports so tests load the same
 * catalog the app does.
 */
import { plugin } from 'bun';
import path from 'node:path';

const GLOB_CALL =
  /import\.meta\.glob(?:<[^>]*>)?\(\s*'([^']+)'\s*,\s*\{\s*eager:\s*true,\s*import:\s*'default',?\s*\}\s*\)/g;

plugin({
  name: 'vite-import-meta-glob',
  setup(build) {
    build.onLoad({ filter: /[\\/]src[\\/]i18n[\\/][^\\/]+\.ts$/ }, async ({ path: file }) => {
      const source = await Bun.file(file).text();
      const dir = path.dirname(file);
      const imports: string[] = [];
      const contents = source.replace(GLOB_CALL, (_match, pattern: string) => {
        const matches = [...new Bun.Glob(pattern).scanSync({ cwd: dir })].sort();
        const entries = matches.map((match) => {
          const name = `__glob_${imports.length}`;
          const relative = `./${match.split(path.sep).join('/').replace(/^\.\//, '')}`;
          imports.push(`import ${name} from ${JSON.stringify(path.join(dir, match))};`);
          return `${JSON.stringify(relative)}: ${name}`;
        });
        return `{ ${entries.join(', ')} }`;
      });
      return { contents: `${imports.join('\n')}\n${contents}`, loader: 'ts' };
    });
  },
});
