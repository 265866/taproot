import type { ReactNode } from 'react';
import styles from './PlaceholderPage.module.scss';

/** Page frame for a section whose real page has not landed yet. */
export function PlaceholderPage({
  title,
  description,
  children,
}: {
  title: string;
  description: string;
  children?: ReactNode;
}) {
  return (
    <div className={styles.page}>
      <h1 className={styles.title}>{title}</h1>
      <p className={styles.description}>{description}</p>
      {children}
    </div>
  );
}
