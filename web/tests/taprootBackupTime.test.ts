import { describe, expect, test } from 'bun:test';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { createInstance } from 'i18next';
import { I18nextProvider } from 'react-i18next';
import { NextBackup } from '@/features/taproot-backups/NextBackup';
import {
  formatBrowserTime,
  formatCreatedAt,
  formatServerTime,
  offsetLabel,
  rfc3339OffsetSeconds,
  serverZoneLabel,
} from '@/features/taproot-backups/model/schedule';
import { BackupsSection } from '@/features/taproot-settings/TaprootSettingsPage';
import { backupsForm } from '@/features/taproot-settings/model/settingsForm';
import { en } from '@/i18n/resources';
import type { TaprootSettings } from '@/services/api/taproot';

const i18n = createInstance();
// As in src/i18n/index.ts: React escapes, so i18next must not turn `/` into `&#x2F;`.
await i18n.init({
  lng: 'en',
  resources: { en: { translation: en } },
  interpolation: { escapeValue: false },
});
const withI18n = (element: ReturnType<typeof createElement>) =>
  // ICU puts a narrow no-break space before AM and PM; compare with plain spaces.
  renderToStaticMarkup(createElement(I18nextProvider, { i18n }, element)).replace(/\s/g, ' ');
const plain = (s: string) => s.replace(/\s/g, ' ');

const utc = { timeZone: 'Etc/UTC', utcOffsetSeconds: 0 };

describe('zone labels', () => {
  test('offsets read as UTC, UTC+hh:mm, or UTC-hh:mm', () => {
    expect(offsetLabel(0)).toBe('UTC');
    expect(offsetLabel(19800)).toBe('UTC+05:30');
    expect(offsetLabel(-12600)).toBe('UTC-03:30');
  });

  test('the server zone is its name, or its offset when it has none', () => {
    expect(serverZoneLabel(utc)).toBe('Etc/UTC');
    expect(serverZoneLabel({ timeZone: '', utcOffsetSeconds: 3600 })).toBe('UTC+01:00');
  });

  test('RFC 3339 offsets parse, including Z, and a time without one is null', () => {
    expect(rfc3339OffsetSeconds('2026-11-09T00:00:00Z')).toBe(0);
    expect(rfc3339OffsetSeconds('2027-03-15T00:00:00-04:00')).toBe(-14400);
    expect(rfc3339OffsetSeconds('2026-11-09T00:00:00+05:30')).toBe(19800);
    expect(rfc3339OffsetSeconds('2026-11-09T00:00:00')).toBeNull();
  });
});

describe('next backup times', () => {
  test('server time uses the server zone, not the browser zone', () => {
    const got = formatServerTime('2026-11-09T00:00:00Z', utc, 'en-US');
    expect(plain(got.time)).toBe('Nov 9, 2026, 12:00 AM');
    expect(got.zone).toBe('Etc/UTC');
    const local = formatBrowserTime('2026-11-09T00:00:00Z', 'en-US', 'America/Los_Angeles');
    expect(plain(local.time)).toBe('Nov 8, 2026, 4:00 PM');
    expect(local.zone).toBe('America/Los_Angeles');
  });

  test('a named zone follows its own DST rules', () => {
    // New York moves to EDT on 2027-03-14, so midnight the next day is 04:00 UTC.
    const got = formatServerTime(
      '2027-03-15T00:00:00-04:00',
      { timeZone: 'America/New_York', utcOffsetSeconds: -18000 },
      'en-US'
    );
    expect(plain(got.time)).toBe('Mar 15, 2027, 12:00 AM');
  });

  test('without a name, the offset written into the time wins over the current one', () => {
    // The server reported EST (-05:00) now, but the run falls after the change to EDT.
    const got = formatServerTime(
      '2027-03-15T00:00:00-04:00',
      { timeZone: '', utcOffsetSeconds: -18000 },
      'en-US'
    );
    expect(plain(got.time)).toBe('Mar 15, 2027, 12:00 AM');
    expect(got.zone).toBe('UTC-04:00');
  });

  test('a zone this browser does not know falls back to the offset and keeps the name', () => {
    const got = formatServerTime(
      '2026-11-09T00:00:00+01:00',
      { timeZone: 'Mars/Olympus_Mons', utcOffsetSeconds: 3600 },
      'en-US'
    );
    expect(plain(got.time)).toBe('Nov 9, 2026, 12:00 AM');
    expect(got.zone).toBe('Mars/Olympus_Mons');
  });

  test('creation times carry the zone they are shown in', () => {
    expect(plain(formatCreatedAt('2026-11-08T08:00:00Z', 'en-US', 'America/New_York'))).toBe(
      'Nov 8, 2026, 3:00 AM EST'
    );
  });
});

describe('rendered schedule', () => {
  const backups: TaprootSettings['backups'] = {
    dailyAt: '00:00',
    keep: 14,
    nextRunAt: '2026-11-09T00:00:00Z',
  };

  test('NextBackup labels both lines with their zones', () => {
    const html = withI18n(
      createElement(NextBackup, {
        nextRunAt: backups.nextRunAt!,
        zone: utc,
        browserTimeZone: 'Asia/Tokyo',
      })
    );
    expect(html).toContain('Next backup');
    expect(html).toContain('<dt>Server time</dt><dd>Nov 9, 2026, 12:00 AM (Etc/UTC)</dd>');
    expect(html).toContain('<dt>Your time</dt><dd>Nov 9, 2026, 9:00 AM (Asia/Tokyo)</dd>');
  });

  test('the Settings field names the server zone and shows the next backup', () => {
    const html = withI18n(
      createElement(BackupsSection, { settings: backups, zone: utc, save: async () => {} })
    );
    expect(html).toContain('server time (Etc/UTC)');
    expect(html).toContain('Server time');
    expect(html).toContain('Your time');
    expect(html).toContain('value="00:00"');
  });

  test('with the schedule off, Settings shows no next backup and offers 00:00', () => {
    const off = { dailyAt: 'off', keep: 14 };
    const html = withI18n(
      createElement(BackupsSection, {
        settings: off,
        zone: { timeZone: '', utcOffsetSeconds: 0 },
        save: async () => {},
      })
    );
    expect(html).toContain('server time (UTC)');
    expect(html).not.toContain('Next backup');
    expect(backupsForm(off).dailyAt).toBe('00:00');
  });
});
