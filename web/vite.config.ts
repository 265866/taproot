import { defineConfig, type ProxyOptions } from 'vite';
import react from '@vitejs/plugin-react';
import { viteSingleFile } from 'vite-plugin-singlefile';
import path from 'path';
import { execSync } from 'child_process';
import fs from 'fs';

// Get version from environment, git tag, or package.json
function getVersion(): string {
  // 1. Environment variable (set by GitHub Actions)
  if (process.env.VERSION) {
    return process.env.VERSION;
  }

  // 2. Try git tag. No shell fallback: under Windows cmd, `echo ""` printed literal quotes.
  for (const command of ['git describe --tags --exact-match', 'git describe --tags']) {
    try {
      const gitTag = execSync(command, {
        encoding: 'utf8',
        stdio: ['ignore', 'pipe', 'ignore'],
      }).trim();
      if (gitTag) {
        return gitTag;
      }
    } catch {
      // Git not available or no tags
    }
  }

  // 3. Fall back to package.json version
  try {
    const pkg = JSON.parse(fs.readFileSync(path.resolve(__dirname, 'package.json'), 'utf8'));
    if (pkg.version && pkg.version !== '0.0.0') {
      return pkg.version;
    }
  } catch {
    // package.json not readable
  }

  return 'dev';
}

// `bun run dev` proxies the API prefixes to a local taproot server. The production build is
// served by that server on the same origin and needs no proxy.
function devProxy(): Record<string, ProxyOptions> {
  const target = process.env.TAPROOT_DEV_SERVER || 'http://127.0.0.1:8317';
  // Dev only: until the access guard injects the management key itself, a local server started
  // with MANAGEMENT_PASSWORD needs the key added here. It never reaches the built bundle.
  const key = process.env.TAPROOT_DEV_MANAGEMENT_KEY;
  const options: ProxyOptions = {
    target,
    changeOrigin: true,
    headers: key ? { Authorization: `Bearer ${key}` } : undefined,
  };
  return { '/v8': options, '/v0': options, '/taproot': options };
}

// https://vitejs.dev/config/
export default defineConfig({
  plugins: [
    react(),
    viteSingleFile({
      removeViteModuleLoader: true,
    }),
  ],
  define: {
    __APP_VERSION__: JSON.stringify(getVersion()),
  },
  server: {
    proxy: devProxy(),
  },
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
  css: {
    modules: {
      localsConvention: 'camelCase',
      generateScopedName: '[name]__[local]___[hash:base64:5]',
    },
    preprocessorOptions: {
      scss: {
        additionalData: `@use "@/styles/variables.scss" as *;`,
      },
    },
  },
  build: {
    target: 'es2020',
    outDir: 'dist',
    assetsInlineLimit: 100000000,
    chunkSizeWarningLimit: 100000000,
    cssCodeSplit: false,
    rolldownOptions: {
      output: {
        codeSplitting: false,
      },
    },
  },
});
