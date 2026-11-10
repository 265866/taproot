import { navBadgeText, type NavBadge } from '@/stores/useAttentionStore';

/** The visible badge on a nav item. Screen readers get its meaning from the link's name instead. */
export function NavBadgeMark({ badge }: { badge: NavBadge | undefined }) {
  if (!badge) return null;
  return (
    <span className={`nav-badge nav-badge-${badge.severity}`} aria-hidden="true">
      {navBadgeText(badge)}
    </span>
  );
}
