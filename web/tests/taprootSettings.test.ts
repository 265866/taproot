import { describe, expect, test } from 'bun:test';
import {
  backupsForm,
  backupsUpdate,
  formatSize,
  ingestUpdate,
  keepsForever,
  parseSize,
  retentionForm,
  retentionUpdate,
  type RetentionForm,
} from '@/features/taproot-settings/model/settingsForm';
import { en } from '@/i18n/resources';

describe('sizes', () => {
  test('parse with 1024-based units and a gigabyte default', () => {
    expect(parseSize('20 GB')).toBe(21_474_836_480);
    expect(parseSize('20')).toBe(21_474_836_480);
    expect(parseSize('512mb')).toBe(536_870_912);
    expect(parseSize('512 MiB')).toBe(536_870_912);
    expect(parseSize('1.5TB')).toBe(1_649_267_441_664);
    expect(parseSize('100 KB')).toBe(102_400);
    expect(parseSize('2048 B')).toBe(2048);
  });

  test('reject anything else', () => {
    for (const bad of ['', 'GB', '-1 GB', '20 GBs', '20 PB', 'twenty', '1e3 MB', '20 G']) {
      expect(parseSize(bad)).toBeNull();
    }
  });

  test('format back in the largest unit and round-trip', () => {
    expect(formatSize(21_474_836_480)).toBe('20 GB');
    expect(formatSize(536_870_912)).toBe('512 MB');
    expect(formatSize(1_649_267_441_664)).toBe('1.5 TB');
    expect(formatSize(1000)).toBe('1000 B');
    for (const bytes of [21_474_836_480, 536_870_912, 5 * 1024 ** 3]) {
      expect(parseSize(formatSize(bytes))).toBe(bytes);
    }
  });
});

describe('request log retention', () => {
  const server = {
    maxDays: 14,
    maxBytes: 21_474_836_480,
    excludePaths: [],
    keepUnrouted404: false,
  };

  test('round-trips the server values and recording state unchanged', () => {
    expect(retentionUpdate(retentionForm(server, true))).toEqual({
      ok: true,
      update: { requestLogs: server, recording: true },
    });
    expect(retentionUpdate(retentionForm(server, false))).toMatchObject({
      update: { recording: false },
    });
  });

  test('an unreadable config.yaml leaves recording alone', () => {
    const form = retentionForm(server, null);
    expect(form.recording).toBeNull();
    expect(retentionUpdate(form)).toEqual({
      ok: true,
      update: { requestLogs: server, recording: null },
    });
  });

  test('keeps an odd byte budget exactly unless the size text is edited', () => {
    // 1,500,000,000 bytes shows as "1.4 GB", which parses back to 1,503,238,554.
    const odd = retentionForm({ ...server, maxBytes: 1_500_000_000 }, true);
    expect(odd.size).toBe('1.4 GB');
    expect(retentionUpdate({ ...odd, days: '30' })).toMatchObject({
      ok: true,
      update: { requestLogs: { maxDays: 30, maxBytes: 1_500_000_000 } },
    });
    // Choosing "Keep forever" and back with the same text still keeps it.
    expect(retentionUpdate({ ...odd, size: ' 1.4 GB ' })).toMatchObject({
      update: { requestLogs: { maxBytes: 1_500_000_000 } },
    });
    expect(retentionUpdate({ ...odd, size: '1.5 GB' })).toMatchObject({
      update: { requestLogs: { maxBytes: 1_610_612_736 } },
    });
    expect(retentionUpdate({ ...odd, sizeLimited: false })).toMatchObject({
      update: { requestLogs: { maxBytes: -1 } },
    });
  });

  test('negative limits mean keep forever and come back as -1', () => {
    const form = retentionForm({ ...server, maxDays: -1, maxBytes: -1 }, true);
    expect(form.daysLimited).toBe(false);
    expect(form.sizeLimited).toBe(false);
    expect(keepsForever(form)).toBe(true);
    expect(retentionUpdate(form)).toMatchObject({
      ok: true,
      update: { requestLogs: { maxDays: -1, maxBytes: -1 } },
    });
  });

  test('keeps forever only when neither limit is set', () => {
    expect(keepsForever(retentionForm(server, true))).toBe(false);
    expect(keepsForever(retentionForm({ ...server, maxDays: -1 }, true))).toBe(false);
    expect(keepsForever(retentionForm({ ...server, maxBytes: -1 }, true))).toBe(false);
    expect(keepsForever(retentionForm({ ...server, maxDays: 0, maxBytes: 0 }, true))).toBe(true);
  });

  const base: RetentionForm = retentionForm(server, true);

  test('validates days, size, and exclude rules', () => {
    expect(retentionUpdate({ ...base, days: '0' })).toMatchObject({ error: 'days_range' });
    expect(retentionUpdate({ ...base, days: '2.5' })).toMatchObject({ error: 'days_range' });
    expect(retentionUpdate({ ...base, days: '3651' })).toMatchObject({ error: 'days_range' });
    // Fields behind "Keep forever" are not validated.
    expect(retentionUpdate({ ...base, daysLimited: false, days: 'x' }).ok).toBe(true);
    expect(retentionUpdate({ ...base, size: 'lots' })).toMatchObject({ error: 'size_format' });
    expect(retentionUpdate({ ...base, size: '512 KB' })).toMatchObject({ error: 'size_min' });
    expect(retentionUpdate({ ...base, excludePaths: 'api/hello' })).toMatchObject({
      error: 'exclude_format',
      detail: 'api/hello',
    });
  });

  test('cleans exclude rules line by line', () => {
    const result = retentionUpdate({
      ...base,
      excludePaths: '  HEAD   /api/hello \n\n/v1/models\n GET /api/*',
      keepUnrouted404: true,
    });
    expect(result).toEqual({
      ok: true,
      update: {
        requestLogs: {
          maxDays: 14,
          maxBytes: 21_474_836_480,
          excludePaths: ['HEAD /api/hello', '/v1/models', 'GET /api/*'],
          keepUnrouted404: true,
        },
        recording: true,
      },
    });
  });
});

describe('backup schedule', () => {
  test('round-trips a daily time and keep', () => {
    expect(backupsUpdate(backupsForm({ dailyAt: '03:13', keep: 14 }))).toEqual({
      ok: true,
      update: { dailyAt: '03:13', keep: 14 },
    });
  });

  test('off keeps a default time in the form and sends "off"', () => {
    const form = backupsForm({ dailyAt: 'off', keep: 3 });
    expect(form).toEqual({ scheduled: false, dailyAt: '00:00', keep: '3' });
    expect(backupsUpdate(form)).toEqual({ ok: true, update: { dailyAt: 'off', keep: 3 } });
  });

  test('validates the time and the count', () => {
    const form = backupsForm({ dailyAt: '03:13', keep: 14 });
    for (const time of ['24:00', '3:13', '03:60', 'noon']) {
      expect(backupsUpdate({ ...form, dailyAt: time })).toMatchObject({ error: 'time_format' });
    }
    expect(backupsUpdate({ ...form, dailyAt: '23:59' }).ok).toBe(true);
    expect(backupsUpdate({ ...form, dailyAt: '00:00' }).ok).toBe(true);
    for (const keep of ['0', '-1', '1.5', '', '1001']) {
      expect(backupsUpdate({ ...form, keep })).toMatchObject({ error: 'keep_range' });
    }
  });
});

describe('poll interval', () => {
  test('accepts Go durations and 0', () => {
    for (const value of ['15m', '1h30m', '90s', '0', ' 2h ', '1.5h']) {
      expect(ingestUpdate(value)).toEqual({ ok: true, update: { pollInterval: value.trim() } });
    }
  });
  test('rejects what Go would reject', () => {
    for (const value of ['', '15', 'soon', '-5m', '15 m', '1d']) {
      expect(ingestUpdate(value)).toMatchObject({ ok: false, error: 'duration_format' });
    }
  });
});

test('every validation error has copy', () => {
  const errors = en.taproot_settings.errors as Record<string, string>;
  for (const key of [
    'days_range',
    'size_format',
    'size_min',
    'exclude_format',
    'keep_range',
    'time_format',
    'duration_format',
  ]) {
    expect(errors[key]?.length).toBeGreaterThan(0);
  }
});
