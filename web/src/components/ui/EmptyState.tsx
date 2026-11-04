import type { ReactNode } from 'react';
import { useEmailMask } from '@/hooks/useEmailMask';
import { IconInbox } from './icons';

interface EmptyStateProps {
  title: string;
  description?: string;
  action?: ReactNode;
}

export function EmptyState({ title, description, action }: EmptyStateProps) {
  // Titles and descriptions often carry server error text.
  const { mask } = useEmailMask();
  return (
    <div className="empty-state">
      <div className="empty-content">
        <div className="empty-icon" aria-hidden="true">
          <IconInbox size={20} />
        </div>
        <div>
          <div className="empty-title">{mask(title)}</div>
          {description && <div className="empty-desc">{mask(description)}</div>}
        </div>
      </div>
      {action && <div className="empty-action">{action}</div>}
    </div>
  );
}
