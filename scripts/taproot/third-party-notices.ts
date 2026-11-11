#!/usr/bin/env bun
// Regenerates THIRD_PARTY_NOTICES.md: the Go modules linked into cmd/server
// (linux, darwin, and windows builds, reported by go-licenses), the Rust
// crates in the request-log codec library (Linux builds, reported by cargo
// metadata), and the dashboard's production npm dependencies (reported by
// license-checker), with each package's license text and, where it ships one,
// its NOTICE file. LGPL-3.0 crates also get the section that license requires,
// and any other copyleft license, or one copyleft.ts does not know, stops the
// generator. Run it from anywhere in the repository after go.mod,
// web/package.json, web/bun.lock, the codec's Cargo.lock, or licenses/
// changes; CI fails when the file is stale:
//
//   bun scripts/taproot/third-party-notices.ts
import { existsSync, mkdtempSync, readdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { basename, dirname, join } from 'node:path';
import { copyleft, type Copyleft } from './copyleft';

const GO_LICENSES = 'github.com/google/go-licenses/v2@v2.0.1';
const LICENSE_CHECKER = 'license-checker@25.0.1';
const TARGETS = ['linux/amd64', 'darwin/arm64', 'windows/amd64'];
const CODEC_DIR = 'internal/taproot/reqlog/codec/native';
// Taproot's codec crate itself, covered by LICENSE.
const CODEC_PACKAGE = 'taproot_codec';
const RUST_TARGETS = ['x86_64-unknown-linux-gnu', 'aarch64-unknown-linux-gnu'];
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
  // Rust crates only: where the source is and which crates link it.
  crate?: { repository: string; checksum: string; dependents: string[] };
};

// The GNU licenses in licenses/, which the image ships in /taproot/licenses.
const LGPL_FILE = 'licenses/LGPL-3.0.txt';
const GPL_FILE = 'licenses/GPL-3.0.txt';

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
        // On Windows, go-licenses builds the URL's path part with backslashes.
        byName.set(name, {
          name,
          version,
          license,
          url: url.replaceAll('\\', '/'),
          text: normalize(text),
          notice,
        });
      }
    }
  }
  return [...byName.values()];
}

type CargoMetadata = {
  packages: {
    id: string;
    name: string;
    version: string;
    license: string | null;
    license_file: string | null;
    manifest_path: string;
    repository: string | null;
  }[];
  resolve: {
    nodes: { id: string; deps: { pkg: string; dep_kinds: { kind: string | null }[] }[] }[];
  };
};

// Reads the named files of a package directory. With more than one file,
// each text is headed by its file name.
function readFiles(dir: string, files: string[]): string {
  const texts = files.map((f) => normalize(readFileSync(join(dir, f), 'utf8')));
  return texts.length === 1 ? texts[0] : texts.map((t, i) => `${files[i]}:\n\n${t}`).join('\n\n');
}

// The codec crate's normal dependencies, which the static library links or,
// for procedural macros, which generate part of its code. Build and dev
// dependencies are left out.
function rustCrates(): Entry[] {
  const dir = join(root, CODEC_DIR);
  const byId = new Map<string, Entry>();
  // The SHA-256 of each crates.io package, from Cargo.lock, keyed by name@version.
  const checksums = new Map<string, string>();
  for (const block of readFileSync(join(dir, 'Cargo.lock'), 'utf8').split('[[package]]')) {
    const field = (key: string) => block.match(new RegExp(`^${key} = "([^"]*)"$`, 'm'))?.[1];
    const [name, version, checksum] = [field('name'), field('version'), field('checksum')];
    if (name && version && checksum) checksums.set(`${name}@${version}`, checksum);
  }
  // The crates that depend on each crate, keyed by package ID.
  const dependents = new Map<string, Set<string>>();
  for (const target of RUST_TARGETS) {
    const meta = JSON.parse(
      run(
        ['cargo', 'metadata', '--locked', '--format-version', '1', '--filter-platform', target],
        dir
      )
    ) as CargoMetadata;
    const packages = new Map(meta.packages.map((p) => [p.id, p]));
    const nodes = new Map(meta.resolve.nodes.map((n) => [n.id, n]));
    const self = meta.packages.find((p) => p.name === CODEC_PACKAGE);
    if (!self) throw new Error(`cargo metadata has no ${CODEC_PACKAGE} package`);
    const seen = new Set<string>([self.id]);
    const queue = [self.id];
    for (let id = queue.shift(); id !== undefined; id = queue.shift()) {
      const parent = packages.get(id);
      for (const dep of nodes.get(id)?.deps ?? []) {
        if (!dep.dep_kinds.some((k) => k.kind === null)) continue;
        if (parent) {
          const set = dependents.get(dep.pkg) ?? new Set<string>();
          set.add(`${parent.name} ${parent.version}`);
          dependents.set(dep.pkg, set);
        }
        if (seen.has(dep.pkg)) continue;
        seen.add(dep.pkg);
        queue.push(dep.pkg);
      }
    }
    seen.delete(self.id);
    for (const id of seen) {
      const pkg = packages.get(id);
      if (!pkg || byId.has(id)) continue;
      const crateDir = dirname(pkg.manifest_path);
      const files = readdirSync(crateDir, { withFileTypes: true })
        .filter((d) => d.isFile())
        .map((d) => d.name)
        .sort();
      const licenseFiles = files.filter((f) => /^(license|copying)/i.test(f));
      if (pkg.license_file && !licenseFiles.includes(pkg.license_file)) {
        licenseFiles.push(pkg.license_file);
      }
      const noticeFiles = files.filter((f) => /^notice/i.test(f));
      byId.set(id, {
        name: pkg.name,
        version: pkg.version,
        license: pkg.license ?? `see ${pkg.license_file ?? 'its source'}`,
        url: `https://crates.io/crates/${pkg.name}/${pkg.version}`,
        text: readFiles(crateDir, licenseFiles),
        notice: readFiles(crateDir, noticeFiles),
        crate: {
          repository: pkg.repository ?? '',
          checksum: checksums.get(`${pkg.name}@${pkg.version}`) ?? '',
          dependents: [],
        },
      });
    }
  }
  for (const [id, entry] of byId) {
    if (entry.crate) {
      entry.crate.dependents = [...(dependents.get(id) ?? [])].sort((a, b) =>
        a.localeCompare(b, 'en')
      );
    }
  }
  return [...byId.values()];
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

// The 'en' locale and the version tiebreak keep the order the same on every
// machine; a Rust crate can appear in two versions.
const byNameVersion = (a: Entry, b: Entry) =>
  a.name.localeCompare(b.name, 'en') || a.version.localeCompare(b.version, 'en');

// Packages with the same license text share one copy of it. missing says where
// the license of a package without a license file is stated.
function section(title: string, intro: string, entries: Entry[], missing: string): string {
  const groups = new Map<string, Entry[]>();
  for (const e of entries.sort(byNameVersion)) {
    const key = e.text || `(no license file) ${e.name} ${e.version}`;
    groups.set(key, [...(groups.get(key) ?? []), e]);
  }
  const parts = [`## ${title}\n\n${intro}\n`];
  for (const [key, group] of [...groups].sort((a, b) => byNameVersion(a[1][0], b[1][0]))) {
    const list = group
      .map((e) => `- ${e.name} ${e.version}, ${e.license}${e.url ? ` (${e.url})` : ''}`)
      .join('\n');
    const body = key.startsWith('(no license file)')
      ? `This package ships no license file. ${missing}`
      : `~~~~text\n${key}\n~~~~`;
    const notices = group
      .filter((e) => e.notice)
      .map((e) => `NOTICE file of ${e.name} ${e.version}:\n\n~~~~text\n${e.notice}\n~~~~\n`);
    parts.push([`${list}\n\n${body}\n`, ...notices].join('\n'));
  }
  return parts.join('\n');
}

// The notice that LGPL-3.0 section 4 requires for crates linked into the
// codec library: that they are used and under the LGPL, where their source
// is, and how to relink taproot with a modified copy (section 4(d)(0)).
function lgplSection(crates: Entry[]): string {
  const repo = `https://${SELF_MODULE}`;
  const list = crates
    .sort(byNameVersion)
    .map((e) => {
      const usedBy = e.crate?.dependents.length ? `, used by ${e.crate.dependents.join(', ')}` : '';
      const checksum = e.crate?.checksum
        ? ` The codec's \`Cargo.lock\` pins it by its SHA-256, \`${e.crate.checksum}\`.`
        : '';
      const from = e.crate?.repository ? ` Its upstream repository is ${e.crate.repository}.` : '';
      return `- ${e.name} ${e.version}, ${e.license}${usedBy}. Source: ${e.url}.${checksum}${from}`;
    })
    .join('\n');
  const example = crates[0].name;
  return `## Libraries under the GNU LGPL

Taproot's image, and any server built with the \`taproot_codecs\` build tag, statically links the following crates into its request-log codec library. They are licensed under the GNU Lesser General Public License version 3 (LGPL-3.0), in the exact terms each entry names. Taproot's own code is under the MIT License.

${list}

The LGPL-3.0 is a set of additional permissions on the GNU General Public License version 3. Both texts are in [${LGPL_FILE}](${LGPL_FILE}) and [${GPL_FILE}](${GPL_FILE}), in \`/taproot/licenses/\` in the image, and with each crate's entry below.

The complete source of each taproot release is its tagged source archive on GitHub, listed on ${repo}/releases (\`${repo}/archive/refs/tags/<tag>.tar.gz\`). An image names its release in its \`org.opencontainers.image.version\` label and its commit in \`org.opencontainers.image.revision\`; \`${repo}/archive/<commit>.tar.gz\` is the source of that commit.

To build taproot with a modified copy of one of these crates:

1. Unpack the source of the taproot release or commit you want to run.
2. Put the modified crate in \`${CODEC_DIR}/patches/\`, for example \`${CODEC_DIR}/patches/${example}\`.
3. Add a \`[patch.crates-io]\` entry for it to \`${CODEC_DIR}/Cargo.toml\`:

   \`\`\`toml
   [patch.crates-io]
   ${example} = { path = "patches/${example}" }
   \`\`\`

4. In \`${CODEC_DIR}\`, run \`cargo update -p ${example}\` to record the patched crate in \`Cargo.lock\`.
5. Build the image with \`docker build -f Containerfile.taproot -t taproot .\` from the repository root. Or, on Linux with cgo, a Rust toolchain, and Bun, embed the dashboard with \`bash scripts/taproot/build-web.sh\`, build the codec library with \`(cd ${CODEC_DIR} && cargo build --release --locked && objcopy --remove-section=.llvmbc --remove-section=.llvmcmd target/release/libtaproot_codec.a)\`, and then build the server with \`go build -tags taproot_codecs -o taproot ./cmd/server\`.
`;
}

try {
  const goroot = run(['go', 'env', 'GOROOT'], root).trim();
  // go.mod's go directive, the oldest Go that builds the server, rather than
  // the local toolchain, so that every machine writes the same file.
  const goVersion = (JSON.parse(run(['go', 'mod', 'edit', '-json'], root)) as { Go: string }).Go;
  const go: Entry[] = [
    {
      name: 'Go standard library and runtime',
      version: `go${goVersion}`,
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
  // The image's Rust toolchain, which links the standard library into the
  // codec library. The license texts come from the local toolchain.
  const rustVersion = readFileSync(join(root, 'Containerfile.taproot'), 'utf8').match(
    /^ARG RUST_VERSION=(\S+)$/m
  )?.[1];
  if (!rustVersion) throw new Error('Containerfile.taproot has no ARG RUST_VERSION');
  const sysroot = run(['rustc', '--print', 'sysroot'], root).trim();
  const licenses = join(sysroot, 'share/doc/rust/licenses');
  const rust: Entry[] = [
    {
      name: 'Rust standard library',
      version: rustVersion,
      // core's Unicode tables are under Unicode-3.0.
      license: '(MIT OR Apache-2.0) AND Unicode-3.0',
      url: 'https://www.rust-lang.org/policies/licenses',
      text: readFiles(licenses, ['MIT.txt', 'Apache-2.0.txt', 'Unicode-3.0.txt']),
      notice: '',
    },
    ...rustCrates(),
  ];
  const web = webPackages();
  // A copyleft license other than the LGPL v3 of a Rust crate needs a person
  // to decide how taproot complies, so it stops the generator.
  const lgpl: Entry[] = [];
  for (const [kind, entries] of [
    ['Go module', go],
    ['Rust crate', rust],
    ['npm package', web],
  ] as const) {
    for (const e of entries) {
      let obligation: Copyleft;
      try {
        obligation = copyleft(String(e.license));
      } catch (err) {
        throw new Error(
          `${kind} ${e.name} ${e.version}: ${(err as Error).message}. Check its license, then add the id to scripts/taproot/copyleft.ts if it only asks for the notice.`
        );
      }
      if (obligation === 'lgpl' && kind === 'Rust crate') lgpl.push(e);
      else if (obligation !== 'none') {
        throw new Error(
          `${kind} ${e.name} ${e.version} is under ${e.license}, which these notices do not cover. Decide how taproot complies with it, then extend scripts/taproot/third-party-notices.ts.`
        );
      }
    }
  }
  // LGPL-3.0 section 4(b) requires the GNU GPL v3 to accompany the LGPL text.
  const gpl = normalize(readFileSync(join(root, GPL_FILE), 'utf8'));
  if (lgpl.length > 0 && !existsSync(join(root, LGPL_FILE))) {
    throw new Error(`${LGPL_FILE} is missing; the image must ship it with the LGPL crates`);
  }
  for (const e of lgpl) {
    if (e.text) {
      e.text = `${e.text}\n\nThe GNU General Public License v3, which the LGPL v3 supplements:\n\n${gpl}`;
    }
  }
  const out = [
    '# Third-party notices',
    '',
    "Taproot's server binary links the Go modules below. In the image, it also links the request-log codec library, which is built from the Rust crates below. The dashboard bundles the npm packages below. Each entry gives its license text and, where the package ships one, its NOTICE file. Taproot's own license is in [LICENSE](LICENSE), and the dashboard's in [web/LICENSE](web/LICENSE).",
    '',
    `This file is generated by \`bun scripts/taproot/third-party-notices.ts\` with ${GO_LICENSES}, \`cargo metadata\`, and ${LICENSE_CHECKER}. Do not edit it by hand.`,
    '',
    ...(lgpl.length > 0 ? [lgplSection(lgpl)] : []),
    section(
      'Go modules',
      `The modules linked into \`cmd/server\` for ${TARGETS.join(', ')}.`,
      go,
      'Its README states the license above.'
    ),
    section(
      'Rust crates',
      `The crates that \`libtaproot_codec.a\` is built from for ${RUST_TARGETS.join(', ')}: the codec's normal dependencies, including the procedural macros that generate part of its code, and the Rust standard library. The licenses of the standard library's own crates.io dependencies are in \`share/doc/rust/COPYRIGHT-library.html\` of the Rust toolchain.`,
      rust,
      'Its Cargo.toml states the license above.'
    ),
    section(
      'npm packages',
      "The dashboard's production dependencies.",
      web,
      'Its README states the license above.'
    ),
  ].join('\n');
  writeFileSync(join(root, 'THIRD_PARTY_NOTICES.md'), out.trimEnd() + '\n');
  console.log(
    `Wrote THIRD_PARTY_NOTICES.md: ${go.length} Go entries, ${rust.length} Rust entries, ${web.length} npm packages.`
  );
} finally {
  rmSync(tmp, { recursive: true, force: true });
}
