import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';

const source = (path: string) => readFileSync(new URL(path, import.meta.url), 'utf8');
const tokensCSS = source('./tokens.css');
const themeCSS = source('./pearl-river-theme.css');
const shellCSS = source('./shell.css');
const runtimeCSS = source('../components/settings/RuntimeSettings.vue');
const updaterCSS = source('../components/PanelUpdater.vue');
const dashboardCSS = source('../components/Dashboard.vue');
const plansCSS = source('../pages/PlansPage.vue');

type Color = { rgb: number[]; alpha: number };
type Variables = Record<string, string>;

function declarations(source: string, selector: string): Variables {
  const rule = [...source.matchAll(/([^{}]+)\{([^{}]*)\}/g)].find(match =>
    match[1]!.replace(/\/\*[\s\S]*?\*\//g, '').trim() === selector);
  if (!rule) throw new Error(`Missing CSS rule: ${selector}`);
  return Object.fromEntries(rule[2]!.split(';').filter(value => value.includes(':')).map(value => {
    const index = value.indexOf(':');
    return [value.slice(0, index).trim(), value.slice(index + 1).trim()];
  }));
}

function resolve(value: string, variables: Variables): string {
  return value.replace(/var\((--[\w-]+)\)/g, (_, name: string) => {
    if (!variables[name]) throw new Error(`Missing color token: ${name}`);
    return resolve(variables[name]!, variables);
  });
}

function color(value: string, variables: Variables): Color {
  const resolved = resolve(value, variables);
  if (resolved.startsWith('#')) {
    let hex = resolved.slice(1);
    if (hex.length === 3 || hex.length === 4) hex = [...hex].map(part => part + part).join('');
    return { rgb: [0, 2, 4].map(offset => parseInt(hex.slice(offset, offset + 2), 16)),
      alpha: hex.length === 8 ? parseInt(hex.slice(6, 8), 16) / 255 : 1 };
  }
  const parts = resolved.match(/^rgba?\(([^)]+)\)$/)?.[1]?.split(',').map(Number);
  if (!parts || parts.length < 3) throw new Error(`Unsupported color: ${resolved}`);
  return { rgb: parts.slice(0, 3), alpha: parts[3] ?? 1 };
}

function paint(foreground: Color, background: Color): Color {
  return { rgb: foreground.rgb.map((channel, index) => channel * foreground.alpha +
    background.rgb[index]! * (1 - foreground.alpha)), alpha: 1 };
}

function luminance(value: Color): number {
  return value.rgb.map(channel => channel / 255).map(channel => channel <= 0.04045 ?
    channel / 12.92 : ((channel + 0.055) / 1.055) ** 2.4)
    .reduce((sum, channel, index) => sum + channel * [0.2126, 0.7152, 0.0722][index]!, 0);
}

function contrast(foreground: Color, background: Color): number {
  const first = luminance(paint(foreground, background)), second = luminance(background);
  return (Math.max(first, second) + 0.05) / (Math.min(first, second) + 0.05);
}

const rootTokens = declarations(tokensCSS, ':root');
const light = { ...rootTokens, ...declarations(themeCSS, ':root') };
const dark = { ...light, ...declarations(themeCSS, ':root[data-theme="dark"]') };
const surfaces = ['--canvas', '--surface', '--surface-raised', '--surface-soft',
  '--surface-muted', '--surface-hover', '--surface-table', '--input'];

describe('semantic theme readability', () => {
  for (const [name, variables] of Object.entries({ light, dark })) {
    it(`${name} keeps body, labels and small supporting text readable on every shared surface`, () => {
      const canvas = color('var(--canvas)', variables);
      for (const surface of surfaces) {
        const background = paint(color(`var(${surface})`, variables), canvas);
        for (const ink of ['--text', '--secondary', '--muted', '--accent-text']) {
          expect(contrast(color(`var(${ink})`, variables), background), `${ink} on ${surface}`).toBeGreaterThanOrEqual(4.5);
        }
      }
    });

    it(`${name} keeps status text and enabled control boundaries distinguishable`, () => {
      const surface = paint(color('var(--surface)', variables), color('var(--canvas)', variables));
      for (const [ink, background] of [['--warning', '--warning-soft'], ['--red', '--danger-soft'],
        ['--info', '--info-soft'], ['--success', '--success-soft']]) {
        expect(contrast(color(`var(${ink})`, variables), paint(color(`var(${background})`, variables), surface)), ink)
          .toBeGreaterThanOrEqual(4.5);
      }
      const input = paint(color('var(--input)', variables), surface);
      expect(contrast(color('var(--input-border)', variables), input)).toBeGreaterThanOrEqual(3);
      expect(contrast(color('var(--input-border)', variables), surface)).toBeGreaterThanOrEqual(3);
      expect(contrast(color('var(--accent)', variables), surface)).toBeGreaterThanOrEqual(3);
      expect(contrast(color('var(--accent-contrast)', variables), color('var(--accent)', variables))).toBeGreaterThanOrEqual(4.5);
    });

    it(`${name} isolates text in permanently dark navigation from the content theme`, () => {
      const chrome = { ...variables,
        ...declarations(themeCSS, ':is(.sidebar, .topbar, .mobile-quick-nav)') };
      const background = paint(color('var(--topbar-bg)', chrome), color('var(--canvas)', variables));
      for (const selector of ['.page-breadcrumb', '.breadcrumb-group', '.header-account',
        '.header-account small', '.language-switch select', '.top-actions>.icon']) {
        const ink = declarations(shellCSS, selector).color!;
        expect(contrast(color(ink, chrome), background), selector).toBeGreaterThanOrEqual(4.5);
      }
      const versionInk = declarations(updaterCSS, '.sidebar .version-badge').color!;
      expect(contrast(color(versionInk, chrome), color('var(--surface-raised)', chrome))).toBeGreaterThanOrEqual(4.5);
      expect(contrast(color('var(--accent-text)', chrome), color('var(--surface-hover)', chrome))).toBeGreaterThanOrEqual(4.5);
    });
  }

  it('keeps small text readable even over the darkest wallpaper pixels', () => {
    const layers = resolve(light['--wallpaper-overlay']!, light).match(/rgba\([^)]+\)/g)!.map(value => color(value, light));
    // Every gradient can interpolate between its stops. The darkest composite
    // of all stops over black is a conservative bound for the actual artwork.
    for (const horizontal of layers.slice(0, 3)) {
      for (const vertical of layers.slice(3)) {
        const background = paint(horizontal, paint(vertical, color('#000', light)));
        expect(contrast(color('var(--muted)', light), background)).toBeGreaterThanOrEqual(4.5);
      }
    }
  });

  it('uses readable semantic colors for the login label and pending runtime badge', () => {
    const surface = paint(color('var(--surface)', light), color('var(--canvas)', light));
    const label = declarations(themeCSS, '.login-form .eyebrow, .courtyard-kicker').color!;
    expect(contrast(color(label, light), surface)).toBeGreaterThanOrEqual(4.5);
    const pending = declarations(runtimeCSS, '.runtime-badge.pending');
    expect(contrast(color(pending.color!, light), paint(color(pending.background!, light), surface))).toBeGreaterThanOrEqual(4.5);
  });

  it('keeps hidden-series labels and node identifiers readable instead of fading their text', () => {
    const surface = paint(color('var(--surface)', light), color('var(--canvas)', light));
    for (const [source, selector] of [[dashboardCSS, '.dash-legend button.off'],
      [plansCSS, '.group-cards :deep(.node-id)']]) {
      const style = declarations(source!, selector!);
      const ink = color(style.color ?? 'var(--muted)', light);
      ink.alpha *= Number(style.opacity ?? 1);
      expect(contrast(ink, surface), selector).toBeGreaterThanOrEqual(4.5);
    }
  });
});
