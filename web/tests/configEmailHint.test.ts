import { afterEach, beforeAll, expect, test } from 'bun:test';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import i18n from '../src/i18n/index';
import ConfigSourceEditor from '../src/features/config/components/ConfigSourceEditor';
import { usePrivacyStore } from '../src/stores/usePrivacyStore';

beforeAll(async () => {
  await i18n.changeLanguage('en');
});
afterEach(() => {
  usePrivacyStore.setState({ showEmails: false });
});

const render = (value: string) =>
  renderToStaticMarkup(
    createElement(ConfigSourceEditor, {
      value,
      onChange: () => {},
      theme: 'dark',
      editable: true,
      placeholder: '',
    })
  );

test('the YAML editor says how to edit masked addresses while emails are hidden', () => {
  const yaml = 'proxy-url: socks5://user:pass@proxy.example.com:1080\n';
  expect(render(yaml)).toContain('Show emails to edit them');
  expect(render('port: 8317\n')).not.toContain('Show emails to edit them');
  usePrivacyStore.getState().toggleShowEmails();
  expect(render(yaml)).not.toContain('Show emails to edit them');
});
