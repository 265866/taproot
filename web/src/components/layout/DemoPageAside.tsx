import { Button } from '@/components/ui/Button';
import { SeverityTag } from '@/components/ui/SeverityTag';
import type { Severity } from '@/components/ui/status';
import { PageAside } from './PageAside';
import styles from './DemoPageAside.module.scss';

const ITEMS: { severity: Severity; meta: string; title: string; detail: string; action: string }[] =
  [
    {
      severity: 'blocking',
      meta: 'Account · 44m ago',
      title: 'Codex: dana@example.com is signed out',
      detail: 'Refresh token rejected at 3:52 AM. 0.84 of Codex quota is locked until you sign in.',
      action: 'Sign in again',
    },
    {
      severity: 'waiting',
      meta: 'Access request · 6m ago',
      title: 'Dana’s iPad asks for Proxy access',
      detail: 'dana@github · 100.64.0.23 · tailnet-0000.ts.net',
      action: 'Review',
    },
    {
      severity: 'overdue',
      meta: 'Backups · 2h ago',
      title: 'Last backup was 31 h ago',
      detail: 'The 2:00 AM scheduled run failed: no space left on device.',
      action: 'Back up now',
    },
  ];

/**
 * Development preview of the shell's page rail: `bun run dev` shows this sample beside every page
 * with `?demo-aside` in the address. Production builds leave it out.
 */
export function DemoPageAside() {
  return (
    <PageAside label="Right now">
      <div className={styles.rail}>
        <h2 className={styles.heading}>Needs you</h2>
        <ul className={styles.items}>
          {ITEMS.map((item) => (
            <li key={item.title} className={styles.item}>
              <div className={styles.meta}>
                <SeverityTag severity={item.severity} />
                {item.meta}
              </div>
              <div className={styles.title}>{item.title}</div>
              <div className={styles.detail}>{item.detail}</div>
              <Button variant="secondary" size="sm">
                {item.action}
              </Button>
            </li>
          ))}
        </ul>
        <h2 className={styles.heading}>Pools</h2>
        <div className={styles.placeholder} />
      </div>
    </PageAside>
  );
}
