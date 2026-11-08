import { afterEach, describe, expect, spyOn, test } from 'bun:test';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { createInstance } from 'i18next';
import { I18nextProvider } from 'react-i18next';
import { RetentionSection } from '@/features/taproot-settings/TaprootSettingsPage';
import { saveRecording } from '@/features/taproot-settings/recording';
import {
  retentionForm,
  retentionUpdate,
  saveRetention,
  type Recording,
  type RetentionUpdate,
} from '@/features/taproot-settings/model/settingsForm';
import { en } from '@/i18n/resources';
import type { TaprootSettings } from '@/services/api/taproot';
import { configApi } from '@/services/api/config';
import { useConfigStore } from '@/stores';

const i18n = createInstance();
await i18n.init({ lng: 'en', resources: { en: { translation: en } } });

const limited: TaprootSettings['requestLogs'] = {
  maxDays: 14,
  maxBytes: 20 * 1024 ** 3,
  excludePaths: [],
  keepUnrouted404: false,
};
const forever: TaprootSettings['requestLogs'] = { ...limited, maxDays: -1, maxBytes: -1 };
const diskUse = 3.5 * 1024 ** 3;

const render = (
  settings: TaprootSettings['requestLogs'],
  recording: Recording,
  diskBytes: number | null = diskUse
) =>
  renderToStaticMarkup(
    createElement(
      I18nextProvider,
      { i18n },
      createElement(RetentionSection, {
        settings,
        recording,
        diskBytes,
        putSettings: async () => {
          throw new Error('not saved in a render test');
        },
        applySettings: () => {},
        saveRecording: async () => {},
      })
    )
  );

/** The visible text of each checked radio's label, in order. */
const checkedOptions = (html: string) =>
  [...html.matchAll(/<input type="radio"[^>]*checked=""[^>]*\/><span>([^<]+)<\/span>/g)].map(
    (match) => match[1]
  );

describe('request logs card', () => {
  test('is named for request logs and shows the recording switch', () => {
    const html = render(limited, true);
    expect(html).toContain('Request logs');
    expect(html).not.toContain('Request log retention');
    expect(html).toMatch(/<input type="checkbox" checked=""[^>]*\/>.*Record requests/);
    expect(html).toContain('observability.logs.request-log');
    expect(html).not.toContain('Recording is off');
  });

  test('shows each limit as an explicit choice', () => {
    const html = render(limited, true);
    expect(checkedOptions(html)).toEqual(['Delete after', 'Cap at']);
    expect(html).toContain('value="14"');
    expect(html).toContain('value="20 GB"');
    expect(html).toContain('the oldest logs are deleted first');
    expect(html).not.toContain('Taproot keeps every request log');

    const ageOnly = render({ ...limited, maxBytes: -1 }, true);
    expect(checkedOptions(ageOnly)).toEqual(['Delete after', 'Keep forever']);
    expect(ageOnly).not.toContain('Taproot keeps every request log');
  });

  test('says it keeps every log, with the disk use, when both limits keep forever', () => {
    const html = render(forever, true);
    expect(checkedOptions(html)).toEqual(['Keep forever', 'Keep forever']);
    expect(html).toContain('Taproot keeps every request log. Disk use: 3.50 GB.');
    expect(html).not.toContain('the oldest logs are deleted first');
  });

  test('leaves the disk use out when the stats could not be read', () => {
    const html = render(forever, true, null);
    expect(html).toContain('Taproot keeps every request log.');
    expect(html).not.toContain('Disk use');
  });

  test('says plainly when recording is off and dims the limits', () => {
    const html = render(forever, false);
    expect(html).toMatch(/<input type="checkbox"(?![^>]*checked)[^>]*\/>.*Record requests/);
    expect(html).toContain('Recording is off.');
    expect(html).toContain('data-dimmed=""');
    // The limits still prune stored logs, so they stay editable.
    expect(html).not.toMatch(/type="radio"[^>]*disabled/);
    expect(render(forever, true)).not.toContain('data-dimmed');
  });

  test('disables the switch when config.yaml could not be read', () => {
    const html = render(limited, null);
    expect(html).toMatch(/<input type="checkbox"[^>]*disabled=""[^>]*\/>.*Record requests/);
    expect(html).toContain('could not read config.yaml');
    expect(html).not.toContain('Recording is off');
    expect(html).not.toContain('data-dimmed');
  });
});

describe('saving the request logs card', () => {
  const run = (
    update: RetentionUpdate,
    saved: Recording,
    fail: 'settings' | 'recording' | null = null
  ) => {
    const calls: string[] = [];
    const result = saveRetention(update, saved, {
      requestLogs: async (requestLogs) => {
        calls.push(`taproot.yaml ${requestLogs.maxDays}`);
        if (fail === 'settings') throw new Error('refused');
        return requestLogs.maxDays;
      },
      recording: async (enabled) => {
        calls.push(`request-log ${enabled}`);
        if (fail === 'recording') throw new Error('config.yaml refused');
      },
      apply: (maxDays) => {
        calls.push(`apply ${maxDays}`);
      },
    });
    return { calls, result };
  };
  const update = (settings: TaprootSettings['requestLogs'], recording: Recording) => {
    const result = retentionUpdate(retentionForm(settings, recording));
    if (!result.ok) throw new Error(result.error);
    return result.update;
  };

  test('writes request-log after taproot.yaml when the switch changed', async () => {
    const { calls, result } = run(update(forever, false), true);
    await result;
    expect(calls).toEqual(['taproot.yaml -1', 'request-log false', 'apply -1']);
  });

  test('applies the saved limits even when request-log fails, and reports the failure', async () => {
    const { calls, result } = run(update(forever, false), true, 'recording');
    await expect(result).rejects.toThrow('config.yaml refused');
    expect(calls).toEqual(['taproot.yaml -1', 'request-log false', 'apply -1']);
  });

  test('leaves request-log alone when the switch did not change or is unknown', async () => {
    for (const [recording, saved] of [
      [true, true],
      [false, false],
      [null, null],
    ] as const) {
      const { calls, result } = run(update(limited, recording), saved);
      await result;
      expect(calls).toEqual(['taproot.yaml 14', 'apply 14']);
    }
  });

  test('does not touch request-log when taproot.yaml refuses the update', async () => {
    const { calls, result } = run(update(limited, false), true, 'settings');
    await expect(result).rejects.toThrow('refused');
    expect(calls).toEqual(['taproot.yaml 14']);
  });
});

describe('writing the recording switch', () => {
  const initial = useConfigStore.getState().config;
  afterEach(() => {
    useConfigStore.setState({ config: initial });
  });

  test('writes request-log through the management API, then updates the config store', async () => {
    useConfigStore.setState({ config: { requestLog: true } });
    const order: string[] = [];
    const put = spyOn(configApi, 'updateRequestLog').mockImplementation(async (enabled) => {
      order.push(`api ${enabled} store ${useConfigStore.getState().config?.requestLog}`);
      return {};
    });
    try {
      await saveRecording(false);
      expect(put).toHaveBeenCalledWith(false);
      expect(order).toEqual(['api false store true']);
      expect(useConfigStore.getState().config?.requestLog).toBe(false);
    } finally {
      put.mockRestore();
    }
  });

  test('leaves the config store alone when the write fails', async () => {
    useConfigStore.setState({ config: { requestLog: true } });
    const put = spyOn(configApi, 'updateRequestLog').mockRejectedValue(new Error('refused'));
    try {
      await expect(saveRecording(false)).rejects.toThrow('refused');
      expect(useConfigStore.getState().config?.requestLog).toBe(true);
    } finally {
      put.mockRestore();
    }
  });
});
