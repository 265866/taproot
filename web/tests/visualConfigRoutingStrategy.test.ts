import { describe, expect, test } from 'bun:test';
import { createElement, useState } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { parse as parseYaml } from 'yaml';
import { useVisualConfig } from '../src/hooks/useVisualConfig';
import { normalizeRoutingStrategy } from '../src/services/api/routing';
import { runVisualConfig } from './helpers/visualConfig';

describe('visual config weighted routing strategy', () => {
  test('recognizes the weighted-round-robin backend value', () => {
    expect(normalizeRoutingStrategy('weighted-round-robin')).toBe('weighted-round-robin');
    expect(normalizeRoutingStrategy('weightedroundrobin')).toBe('weighted-round-robin');
    expect(normalizeRoutingStrategy('wrr')).toBe('weighted-round-robin');
    expect(normalizeRoutingStrategy('fill-first')).toBe('fill-first');
    expect(normalizeRoutingStrategy('fillfirst')).toBe('fill-first');
    expect(normalizeRoutingStrategy('ff')).toBe('fill-first');
    expect(normalizeRoutingStrategy('soonest-reset-first')).toBe('soonest-reset-first');
    expect(normalizeRoutingStrategy('srf')).toBe('soonest-reset-first');
    expect(normalizeRoutingStrategy(undefined)).toBe('round-robin');
  });

  test('writes weighted-round-robin without coercing it to round-robin', () => {
    function Harness() {
      const visualConfig = useVisualConfig();
      const [phase, setPhase] = useState(0);

      if (phase === 0) {
        visualConfig.setVisualValues({ routingStrategy: 'weighted-round-robin' });
        setPhase(1);
      } else {
        return createElement(
          'pre',
          null,
          visualConfig.applyVisualChangesToYaml('routing:\n  strategy: round-robin\n')
        );
      }

      return null;
    }

    const markup = renderToStaticMarkup(createElement(Harness));
    const result = markup.slice('<pre>'.length, -'</pre>'.length);

    expect(parseYaml(result)).toEqual({ routing: { strategy: 'weighted-round-robin' } });
  });
});

describe('visual config reads routing.strategy like the backend', () => {
  test('the Config page and the Routing page agree', () => {
    for (const [stored, expected] of [
      ['fill_first', 'round-robin'],
      ['soonest_reset_first', 'soonest-reset-first'],
      ['FF', 'fill-first'],
    ] as const) {
      expect(
        runVisualConfig(`routing:\n  strategy: ${stored}\n`).visualValues.routingStrategy
      ).toBe(expected);
    }
  });
});
