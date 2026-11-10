import { describe, expect, test } from 'bun:test';
import { isLogAnchorEvicted } from '@/features/taproot-logs/appLogs/hooks/useLogScroller';
import {
  formatUtcOffset,
  serverUtcOffsetMinutes,
} from '@/features/taproot-logs/appLogs/model/logBuffer';
import { parseLogLine } from '@/features/taproot-logs/appLogs/model/logParsing';
import { readWeb } from './helpers/themeTokens';

const panel = readWeb('src/features/taproot-logs/appLogs/AppLogsPanel.tsx');
const styles = readWeb('src/features/taproot-logs/appLogs/AppLogsPanel.module.scss');

describe('app log line parsing', () => {
  test('an address or a duration in message text is not the client IP or the latency', () => {
    const parsed = parseLogLine(
      '[2026-11-10 07:13:33] [309630fa] [error] [conductor.go:88] upstream execution failed: ' +
        'duration=0s err=Post "https://example.com/v1/messages": dial tcp 127.0.0.1:9: connect: refused'
    );
    expect(parsed.ip).toBeUndefined();
    expect(parsed.latency).toBeUndefined();
    expect(parsed.message).toContain('127.0.0.1:9');
  });

  test('pipe-separated text keeps addresses inside a field as text', () => {
    const parsed = parseLogLine(
      '[2026-11-10 07:13:33] [--------] [warn ] retry | dial tcp 100.64.0.7:443 took 3s | giving up'
    );
    expect(parsed.ip).toBeUndefined();
    expect(parsed.latency).toBeUndefined();
    expect(parsed.message).toContain('100.64.0.7:443 took 3s');
  });

  test('gin access lines still give their latency and client IP', () => {
    const parsed = parseLogLine(
      '[2026-11-10 07:13:33] [309630fa] [info ] [gin_logger.go:11] 200 | 1.2s | 100.64.0.7 | POST "/v1/messages"'
    );
    expect(parsed).toMatchObject({ statusCode: 200, latency: '1.2s', ip: '100.64.0.7' });
  });
});

describe('app log scrolling', () => {
  test('a missing anchor is evicted only once the buffer has moved past it', () => {
    expect(isLogAnchorEvicted(100, 101)).toBe(true);
    expect(isLogAnchorEvicted(150, 101)).toBe(false);
  });

  test('a search or filter change starts again at the latest line', () => {
    const scroller = readWeb('src/features/taproot-logs/appLogs/hooks/useLogScroller.ts');
    expect(scroller).toContain('filterKey !== previousFilterKeyRef.current');
    expect(panel).toMatch(/useLogScroller\(\{[^}]*filterKey,/);
  });
});

describe('app log times', () => {
  test('the server offset comes from the newest line and its Unix time', () => {
    const lines = [
      '[2026-11-10 07:13:30] [--------] [info ] a',
      '[2026-11-10 07:13:33] [--------] [info ] b',
      'continuation without a time',
    ];
    const utc = Date.UTC(2026, 10, 10, 7, 13, 33) / 1000;
    expect(serverUtcOffsetMinutes(lines, utc)).toBe(0);
    expect(serverUtcOffsetMinutes(lines, utc + 5 * 3600)).toBe(-300);
    expect(serverUtcOffsetMinutes(lines, utc - 5.5 * 3600)).toBe(330);
    expect(serverUtcOffsetMinutes(lines, undefined)).toBeNull();
    expect(serverUtcOffsetMinutes([], utc)).toBeNull();
    // Off by minutes: the page's lines are not the newest, so it cannot tell.
    expect(serverUtcOffsetMinutes(lines, utc + 600)).toBeNull();
  });

  test('offsets read as UTC±hh:mm', () => {
    expect(formatUtcOffset(0)).toBe('UTC+00:00');
    expect(formatUtcOffset(330)).toBe('UTC+05:30');
    expect(formatUtcOffset(-300)).toBe('UTC−05:00');
  });

  test('the viewer says which clock its lines use', () => {
    expect(panel).toContain('{serverTimeLabel}</div>');
    expect(panel).toContain('title={serverTimeLabel}');
  });
});

describe('app log viewer', () => {
  test('the sub-tab and the search survive opening a request and coming back', () => {
    expect(panel).toContain("const LOG_TAB_PARAM = 'logTab';");
    expect(panel).toContain("const LOG_SEARCH_PARAM = 'logQ';");
    expect(panel).toContain('decodeUrlValue(params.get(LOG_SEARCH_PARAM)');
    expect(panel).not.toContain("useState<TabType>('logs')");
  });

  test('phones wrap each line under its time, whatever the wrap setting', () => {
    const phone = styles.slice(styles.indexOf('@media (max-width: 768px)'));
    expect(phone).toMatch(/\n {2}\.logRow \{\n {4}grid-template-columns: 1fr;/);
    expect(phone).toMatch(/\n {2}\.rowMain \{\n {4}flex-wrap: wrap;\n {4}white-space: normal;/);
  });
});
