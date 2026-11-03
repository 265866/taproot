import { describe, expect, test } from 'bun:test';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { createInstance } from 'i18next';
import { I18nextProvider } from 'react-i18next';
import { ErrorRulesEditor } from '@/features/authFiles/policy/ErrorRulesEditor';
import { readErrorRules, type ErrorRuleDraft } from '@/features/authFiles/policy/errorRules';
import { en } from '@/i18n/resources';

const i18n = createInstance();
await i18n.init({ lng: 'en', resources: { en: { translation: en } } });
const render = (component: ReturnType<typeof createElement>) =>
  renderToStaticMarkup(createElement(I18nextProvider, { i18n }, component));
const rows = readErrorRules([
  { status: 429, match: ['quota'], matchRegex: ['(?i)limit'], action: 'continue-and-cooldown' },
  { status: 500, match: ['busy'], action: 'stop' },
]);
const renderRows = (rules: ErrorRuleDraft[] = rows, disabled = false) =>
  render(createElement(ErrorRulesEditor, { rules, onChange: () => {}, disabled }));

describe('error-rule editor UI', () => {
  test('renders rows, named controls, localized actions and match conditions without JSON editing', () => {
    const html = renderRows();
    expect(html).toContain('Rule 1');
    expect(html).toContain('Rule 2');
    expect(html).toContain('role="group"');
    expect(html).toContain('Status code');
    expect(html).toContain('Continue and cool down');
    expect(html).toContain('Contains text');
    expect(html).toContain('Regular expression');
    expect(html).toContain('Go regular expression');
    expect(html).toContain('Add rule');
    expect(html).toContain('Add condition');
    expect(html).not.toContain('JSON');
    expect(html).not.toContain('<select');
    expect(html).toContain('aria-haspopup="listbox"');
    const ids = [...html.matchAll(/\bid="([^"]+)"/g)].map((match) => match[1]);
    expect(new Set(ids).size).toBe(ids.length);
    for (const label of html.matchAll(/\bfor="([^"]+)"/g)) expect(ids).toContain(label[1]);
  });
  test('keeps list order and disables boundary moves', () => {
    const html = renderRows();
    const ups = [...html.matchAll(/<button\b[^>]*aria-label="Move rule up"[^>]*>/g)];
    const downs = [...html.matchAll(/<button\b[^>]*aria-label="Move rule down"[^>]*>/g)];
    expect(ups).toHaveLength(2);
    expect(downs).toHaveLength(2);
    expect(ups[0][0]).toContain('disabled=""');
    expect(ups[1][0]).not.toContain('disabled=""');
    expect(downs[0][0]).not.toContain('disabled=""');
    expect(downs[1][0]).toContain('disabled=""');
  });
  test('all controls are disabled while saving or disconnected', () => {
    const controls = [
      ...renderRows(rows, true).matchAll(/<(?:input|textarea|select|button)\b[^>]*>/g),
    ];
    expect(controls.length).toBeGreaterThan(10);
    for (const control of controls) expect(control[0]).toContain('disabled=""');
  });
  test('an empty rule list keeps its add action', () => {
    expect(renderRows([])).toContain('No rules.');
    expect(renderRows([])).toContain('Add rule');
  });
  test('unrecognized imported actions are displayed safely instead of silently selected away', () => {
    const html = renderRows([{ ...rows[0], action: '<unknown>' }]);
    expect(html).toContain('&lt;unknown&gt;');
    expect(html).not.toContain('<unknown>');
    expect(html).toContain('>&lt;unknown&gt;</span>');
    expect(html).toContain('aria-label="Action"');
  });
  test('the catalog has complete row labels, errors, and actions', () => {
    const section = (en.auth_files as { error_rules: Record<string, unknown> }).error_rules;
    expect(Object.keys(section.actions as object).sort()).toEqual([
      'continue',
      'continue-and-cooldown',
      'stop',
      'stop-and-cooldown',
    ]);
    for (const [key, value] of Object.entries(section)) {
      if (key === 'actions') continue;
      expect(typeof value === 'string' && value.trim().length > 0).toBe(true);
    }
  });
});
