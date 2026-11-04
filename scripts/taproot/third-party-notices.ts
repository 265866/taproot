#!/usr/bin/env bun
// Regenerates THIRD_PARTY_NOTICES.md: the Go modules linked into cmd/server
// (linux, darwin, and windows builds, reported by go-licenses) and the
// dashboard's production npm dependencies (reported by license-checker),
// with each package's license text and, where it ships one, its NOTICE file.
// Run it from anywhere in the repository after go.mod or web/package.json
// changes:
//
//   bun scripts/taproot/third-party-notices.ts
import { existsSync, mkdtempSync, readdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { basename, dirname, join } from 'node:path';

const GO_LICENSES = 'github.com/google/go-licenses/v2@v2.0.1';
const LICENSE_CHECKER = 'license-checker@25.0.1';
const TARGETS = ['linux/amd64', 'darwin/arm64', 'windows/amd64'];
// Taproot itself, covered by LICENSE.
const SELF_MODULE = 'github.com/265866/taproot';
// Modules whose license is stated only in their README. go-licenses cannot
// read a license from them, so they are listed by hand.
const README_ONLY_GO: Record<string, { license: string; url: string }> = {
  'github.com/mattn/go-localereader': {
    license: 'MIT',
    url: 'https://github.com/mattn/go-localereader#license',
  },
};

type Entry = {
  name: string;
  version: string;
  license: string;
  url: string;
  text: string;
  // The package's NOTICE file, which Apache-2.0 section 4(d) requires to be passed on.
  notice: string;
};

// Some packages ship CRLF license files; git stores this file with LF.
const normalize = (text: string) => text.replace(/\r\n?/g, '\n').trim();

// noticeIn returns the NOTICE file in a package directory, or ''.
function noticeIn(dir: string): string {
  if (!dir || !existsSync(dir)) return '';
  const file = readdirSync(dir).find((f) => /^notice(\.(txt|md))?$/i.test(f));
  return file ? normalize(readFileSync(join(dir, file), 'utf8')) : '';
}

function run(cmd: string[], cwd: string, env: Record<string, string> = {}): string {
  const result = Bun.spawnSync(cmd, { cwd, env: { ...process.env, ...env }, stderr: 'pipe' });
  if (result.exitCode !== 0) {
    throw new Error(`${cmd.join(' ')} failed:\n${result.stderr.toString()}`);
  }
  return result.stdout.toString();
}

const root = run(['git', 'rev-parse', '--show-toplevel'], process.cwd()).trim();
const tmp = mkdtempSync(join(tmpdir(), 'taproot-notices-'));

function goModules(): Entry[] {
  run(['go', 'install', GO_LICENSES], root, { GOBIN: tmp });
  const tool = join(tmp, process.platform === 'win32' ? 'go-licenses.exe' : 'go-licenses');
  const template = join(tmp, 'notices.tpl');
  writeFileSync(
    template,
    '{{range .}}@@@LIB {{.Name}} {{.Version}} {{.LicenseName}} {{.LicenseURL}}\n{{.LicensePath}}\n{{.LicenseText}}\n@@@END\n{{end}}'
  );
  const ignore = [SELF_MODULE, ...Object.keys(README_ONLY_GO)].flatMap((m) => ['--ignore', m]);
  const byName = new Map<string, Entry>();
  for (const target of TARGETS) {
    const [goos, goarch] = target.split('/');
    const out = run([tool, 'report', './cmd/server', '--template', template, ...ignore], root, {
      GOOS: goos,
      GOARCH: goarch,
      CGO_ENABLED: '0',
    });
    for (const block of out.split('@@@END\n')) {
      const match = block.match(/^@@@LIB (\S+) (\S+) (\S+) (\S*)\n(.*)\n([\s\S]*)$/);
      if (!match) continue;
      const [, name, version, license, url, licensePath, text] = match;
      const prev = byName.get(name);
      if (prev && prev.license !== license && !prev.license.includes(license)) {
        prev.license = `${prev.license} AND ${license}`;
      }
      if (!prev) {
        const notice = noticeIn(dirname(licensePath.trim()));
        byName.set(name, { name, version, license, url, text: normalize(text), notice });
      }
    }
  }
  return [...byName.values()];
}

function webPackages(): Entry[] {
  const web = join(root, 'web');
  const self = JSON.parse(readFileSync(join(web, 'package.json'), 'utf8')) as {
    name: string;
    version: string;
  };
  const report = JSON.parse(
    run(['bunx', LICENSE_CHECKER, '--production', '--json', '--start', '.'], web)
  ) as Record<
    string,
    { licenses: string; repository?: string; licenseFile?: string; path?: string }
  >;
  const entries: Entry[] = [];
  for (const [id, info] of Object.entries(report)) {
    const at = id.lastIndexOf('@');
    const name = id.slice(0, at);
    const version = id.slice(at + 1);
    if (name === self.name) continue;
    const file = info.licenseFile ?? '';
    const readmeOnly = file === '' || /^readme/i.test(basename(file));
    entries.push({
      name,
      version,
      license: info.licenses,
      url: info.repository ?? '',
      text: readmeOnly ? '' : normalize(readFileSync(file, 'utf8')),
      notice: noticeIn(info.path ?? ''),
    });
  }
  return entries;
}

// Packages with the same license text share one copy of it.
function section(title: string, intro: string, entries: Entry[]): string {
  const groups = new Map<string, Entry[]>();
  for (const e of entries.sort((a, b) => a.name.localeCompare(b.name))) {
    const key = e.text || `(no license file) ${e.name}`;
    groups.set(key, [...(groups.get(key) ?? []), e]);
  }
  const parts = [`## ${title}\n\n${intro}\n`];
  for (const [key, group] of [...groups].sort((a, b) => a[1][0].name.localeCompare(b[1][0].name))) {
    const list = group
      .map((e) => `- ${e.name} ${e.version}, ${e.license}${e.url ? ` (${e.url})` : ''}`)
      .join('\n');
    const body = key.startsWith('(no license file)')
      ? 'This package ships no license file. Its README states the license above.'
      : `~~~~text\n${key}\n~~~~`;
    const notices = group
      .filter((e) => e.notice)
      .map((e) => `NOTICE file of ${e.name} ${e.version}:\n\n~~~~text\n${e.notice}\n~~~~\n`);
    parts.push([`${list}\n\n${body}\n`, ...notices].join('\n'));
  }
  return parts.join('\n');
}

try {
  const goroot = run(['go', 'env', 'GOROOT'], root).trim();
  const goVersion = run(['go', 'env', 'GOVERSION'], root).trim();
  const go: Entry[] = [
    {
      name: 'Go standard library and runtime',
      version: goVersion,
      license: 'BSD-3-Clause',
      url: 'https://go.dev/LICENSE',
      text: normalize(readFileSync(join(goroot, 'LICENSE'), 'utf8')),
      notice: '',
    },
    ...goModules(),
    ...Object.entries(README_ONLY_GO).map(([name, info]) => ({
      name,
      version: run(['go', 'list', '-m', '-f', '{{.Version}}', name], root).trim(),
      license: info.license,
      url: info.url,
      text: '',
      notice: '',
    })),
  ];
  const web = webPackages();
  const out = [
    '# Third-party notices',
    '',
    "Taproot's server binary links the Go modules below, and its dashboard bundles the npm packages below. Each entry gives its license text and, where the package ships one, its NOTICE file. Taproot's own license is in [LICENSE](LICENSE), and the dashboard's in [web/LICENSE](web/LICENSE).",
    '',
    `This file is generated by \`bun scripts/taproot/third-party-notices.ts\` with ${GO_LICENSES} and ${LICENSE_CHECKER}. Do not edit it by hand.`,
    '',
    section(
      'Go modules',
      `The modules linked into \`cmd/server\` for ${TARGETS.join(', ')}.`,
      go
    ),
    section('npm packages', "The dashboard's production dependencies.", web),
  ].join('\n');
  writeFileSync(join(root, 'THIRD_PARTY_NOTICES.md'), out.trimEnd() + '\n');
  console.log(`Wrote THIRD_PARTY_NOTICES.md: ${go.length} Go entries, ${web.length} npm packages.`);
} finally {
  rmSync(tmp, { recursive: true, force: true });
}
