import { readFileSync } from 'node:fs';
import { join } from 'node:path';

export const readWeb = (path: string) => readFileSync(join(import.meta.dir, '../..', path), 'utf8');

export type Rgba = { r: number; g: number; b: number; a: number };

/**
 * Raw custom properties declared in every top-level block for `selector` (anchored at a line start,
 * so `.vizRoot` does not also match a theme-scoped block and the indented `:root` inside the
 * reduced-motion media query is skipped). Later blocks override earlier ones.
 */
export function rawTokens(source: string, selector: string): Record<string, string> {
  const tokens: Record<string, string> = {};
  let from = 0;
  for (;;) {
    const start = source.indexOf(`\n${selector} {`, from);
    if (start < 0) break;
    const body = source.slice(start, source.indexOf('\n}', start));
    for (const [, name, value] of body.matchAll(/^\s*--([\w-]+):\s*([^;]+);/gm)) {
      tokens[name] = value.trim();
    }
    from = start + 1;
  }
  return tokens;
}

const themes = readWeb('src/styles/themes.scss');
const light = rawTokens(themes, ':root');

/** Every theme's tokens as the browser resolves them: light is `:root`, the others override it. */
export const THEMES: Record<'light' | 'white' | 'dark', Record<string, string>> = {
  light,
  white: { ...light, ...rawTokens(themes, "[data-theme='white']") },
  dark: { ...light, ...rawTokens(themes, "[data-theme='dark']") },
};

/** Resolves `var(--name)` chains and parses a hex or rgba() value; null for anything else. */
export function color(tokens: Record<string, string>, name: string): Rgba | null {
  let value = tokens[name];
  for (let depth = 0; value && depth < 8; depth++) {
    const ref = value.match(/^var\(--([\w-]+)\)$/);
    if (!ref) break;
    value = tokens[ref[1]];
  }
  return value ? parseColor(value) : null;
}

export function mustColor(tokens: Record<string, string>, name: string): Rgba {
  const parsed = color(tokens, name);
  if (!parsed) throw new Error(`--${name} is not a literal color (${tokens[name]})`);
  return parsed;
}

export function parseColor(value: string): Rgba | null {
  const hex = value.match(/^#([0-9a-f]{6})$/i);
  if (hex) {
    const n = parseInt(hex[1], 16);
    return { r: n >> 16, g: (n >> 8) & 255, b: n & 255, a: 1 };
  }
  const rgba = value.match(/^rgba?\(\s*([\d.]+)[,\s]+([\d.]+)[,\s]+([\d.]+)(?:[,\s/]+([\d.]+))?\s*\)$/);
  if (rgba) {
    const [r, g, b] = [rgba[1], rgba[2], rgba[3]].map(Number);
    return { r, g, b, a: rgba[4] === undefined ? 1 : Number(rgba[4]) };
  }
  return null;
}

/** Paints a translucent color over an opaque one. */
export function over(top: Rgba, bottom: Rgba): Rgba {
  const mix = (t: number, b: number) => t * top.a + b * (1 - top.a);
  return { r: mix(top.r, bottom.r), g: mix(top.g, bottom.g), b: mix(top.b, bottom.b), a: 1 };
}

export const hex = (value: string): Rgba => {
  const parsed = parseColor(value);
  if (!parsed) throw new Error(`not a color: ${value}`);
  return parsed;
};

const toLinear = (c: number) => {
  const s = c / 255;
  return s <= 0.04045 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
};
const linear = (c: Rgba) => [c.r, c.g, c.b].map(toLinear) as [number, number, number];

export function luminance(c: Rgba): number {
  const [r, g, b] = linear(c);
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}

export function contrast(a: Rgba, b: Rgba): number {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x);
  return (hi + 0.05) / (lo + 0.05);
}

function oklabFromLinear([r, g, b]: [number, number, number]): [number, number, number] {
  const l = Math.cbrt(0.4122214708 * r + 0.5363325363 * g + 0.0514459929 * b);
  const m = Math.cbrt(0.2119034982 * r + 0.6806995451 * g + 0.1073969566 * b);
  const s = Math.cbrt(0.0883024619 * r + 0.2817188376 * g + 0.6299787005 * b);
  return [
    0.2104542553 * l + 0.793617785 * m - 0.0040720468 * s,
    1.9779984951 * l - 2.428592205 * m + 0.4505937099 * s,
    0.0259040371 * l + 0.7827717662 * m - 0.808675766 * s,
  ];
}

/** OKLCH lightness, chroma, and hue in degrees. */
export function oklch(c: Rgba): { l: number; c: number; h: number } {
  const [l, a, b] = oklabFromLinear(linear(c));
  return { l, c: Math.hypot(a, b), h: ((Math.atan2(b, a) * 180) / Math.PI + 360) % 360 };
}

// Machado, Oliveira & Fernandes (2009) dichromacy at severity 1.0, as the dataviz checker uses.
const CVD = {
  protan: [
    [0.152286, 1.052583, -0.204868],
    [0.114503, 0.786281, 0.099216],
    [-0.003882, -0.048116, 1.051998],
  ],
  deutan: [
    [0.367322, 0.860646, -0.227968],
    [0.280085, 0.672501, 0.047413],
    [-0.01182, 0.04294, 0.968881],
  ],
} as const;

/** OKLab ΔE ×100, optionally under simulated protanopia or deuteranopia. */
export function deltaE(a: Rgba, b: Rgba, kind?: keyof typeof CVD): number {
  const view = (c: Rgba): [number, number, number] => {
    const v = linear(c);
    if (!kind) return v;
    const clamp = (x: number) => Math.max(0, Math.min(1, x));
    const [r0, r1, r2] = CVD[kind];
    return [
      clamp(r0[0] * v[0] + r0[1] * v[1] + r0[2] * v[2]),
      clamp(r1[0] * v[0] + r1[1] * v[1] + r1[2] * v[2]),
      clamp(r2[0] * v[0] + r2[1] * v[1] + r2[2] * v[2]),
    ];
  };
  const [x, y] = [oklabFromLinear(view(a)), oklabFromLinear(view(b))];
  return 100 * Math.hypot(x[0] - y[0], x[1] - y[1], x[2] - y[2]);
}
