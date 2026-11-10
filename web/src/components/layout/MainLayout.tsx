import {
  ReactNode,
  RefObject,
  SVGProps,
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type MouseEvent as ReactMouseEvent,
  type SyntheticEvent,
} from 'react';
import { NavLink, useLocation } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { PageTransition } from '@/components/common/PageTransition';
import { MainRoutes } from '@/router/MainRoutes';
import { authFilesApi, pluginsApi } from '@/services/api';
import {
  IconSidebarAccess,
  IconSidebarAccounts,
  IconSidebarBackups,
  IconSidebarConfig,
  IconSidebarDashboard,
  IconSidebarLogs,
  IconSidebarPlugins,
  IconSidebarRouting,
  IconSidebarSettings,
  IconSidebarStore,
  IconSidebarSystem,
  IconSidebarUsage,
  IconChevronDown,
} from '@/components/ui/icons';
import { TaprootMark } from '@/components/common/TaprootMark';
import { useAuthStore, useConfigStore, useNotificationStore, useThemeStore } from '@/stores';
import { AUTH_FILES_CHANGED_EVENT } from '@/features/authFiles/authFilesEvents';
import {
  collectPluginResourceEntries,
  PLUGIN_RESOURCES_REFRESH_EVENT,
  resolvePluginAssetURL,
  type PluginResourceEntry,
} from '@/features/plugins/pluginResources';
import { summarizeRefreshAll, triggerHeaderRefresh } from '@/hooks/useHeaderRefresh';
import { useMediaQuery } from '@/hooks/useMediaQuery';
import { hasOpenLayer } from '@/components/ui/layerStack';
import { FOCUSABLE_SELECTOR, lockScroll, unlockScroll } from '@/components/ui/scrollLock';
import { ROUTES } from '@/router/routes';
import { getSidebarShortcutLabel, isSidebarToggleShortcut } from '@/utils/sidebarShortcut';
import type { Theme } from '@/types';

const sidebarIcons = {
  dashboard: <IconSidebarDashboard size={18} />,
  accounts: <IconSidebarAccounts size={18} />,
  usage: <IconSidebarUsage size={18} />,
  requestLogs: <IconSidebarLogs size={18} />,
  routing: <IconSidebarRouting size={18} />,
  config: <IconSidebarConfig size={18} />,
  plugins: <IconSidebarPlugins size={18} />,
  pluginStore: <IconSidebarStore size={18} />,
  backups: <IconSidebarBackups size={18} />,
  access: <IconSidebarAccess size={18} />,
  settings: <IconSidebarSettings size={18} />,
  system: <IconSidebarSystem size={18} />,
} satisfies Partial<Record<keyof typeof ROUTES, ReactNode>>;

interface SidebarNavLinkItem {
  kind?: 'link';
  path: string;
  labelKey?: string;
  metaKey?: string;
  label?: string;
  meta?: string;
  badge?: number;
  badgeLabel?: string;
  icon: ReactNode;
}

interface SidebarNavDrawerItem {
  kind: 'drawer';
  id: string;
  label: string;
  meta?: string;
  icon: ReactNode;
  children: SidebarNavLinkItem[];
}

type SidebarNavItem = SidebarNavLinkItem | SidebarNavDrawerItem;

const NAV_TOOLTIP_ID = 'sidebar-nav-tooltip';
const SIDEBAR_ID = 'main-navigation';
const MAIN_CONTENT_ID = 'main-content';
const THEME_MENU_ID = 'theme-menu';
// $breakpoint-mobile in variables.scss: at and below it the sidebar is an off-canvas drawer.
const MOBILE_BREAKPOINT_PX = 768;
const NAV_TOOLTIP_VIEWPORT_MARGIN = 8;

interface SidebarNavGroup {
  id: string;
  labelKey: string;
  items: SidebarNavItem[];
}

const flattenNavItems = (items: SidebarNavItem[]): SidebarNavLinkItem[] =>
  items.flatMap((item) => (item.kind === 'drawer' ? item.children : [item]));

/** 点击菜单外或按下 Escape 时关闭弹出菜单 */
function useMenuDismiss(
  open: boolean,
  menuRef: RefObject<HTMLDivElement | null>,
  onClose: (returnFocus: boolean) => void
) {
  useEffect(() => {
    if (!open) {
      return;
    }

    const handlePointerDown = (event: MouseEvent) => {
      if (!menuRef.current?.contains(event.target as Node)) {
        onClose(false);
      }
    };

    const handleEscape = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        onClose(true);
      }
    };

    const handleFocusIn = (event: FocusEvent) => {
      if (!menuRef.current?.contains(event.target as Node)) {
        onClose(false);
      }
    };

    document.addEventListener('mousedown', handlePointerDown);
    document.addEventListener('keydown', handleEscape);
    document.addEventListener('focusin', handleFocusIn);

    return () => {
      document.removeEventListener('mousedown', handlePointerDown);
      document.removeEventListener('keydown', handleEscape);
      document.removeEventListener('focusin', handleFocusIn);
    };
  }, [open, menuRef, onClose]);
}

function PluginSidebarIcon({ src }: { src: string }) {
  const [failed, setFailed] = useState(false);
  const showImage = Boolean(src) && !failed;

  return showImage ? (
    <img src={src} alt="" onError={() => setFailed(true)} />
  ) : (
    <IconSidebarPlugins size={18} />
  );
}

// Header action icons - smaller size for header buttons
const headerIconProps: SVGProps<SVGSVGElement> = {
  width: 16,
  height: 16,
  viewBox: '0 0 24 24',
  fill: 'none',
  stroke: 'currentColor',
  strokeWidth: 2,
  strokeLinecap: 'round',
  strokeLinejoin: 'round',
  'aria-hidden': 'true',
  focusable: 'false',
};

const headerIcons = {
  refresh: (
    <svg {...headerIconProps}>
      <path d="M21 12a9 9 0 1 1-9-9c2.52 0 4.93 1 6.74 2.74L21 8" />
      <path d="M21 3v5h-5" />
    </svg>
  ),
  menu: (
    <svg {...headerIconProps}>
      <path d="M4 7h16" />
      <path d="M4 12h16" />
      <path d="M4 17h16" />
    </svg>
  ),
  close: (
    <svg {...headerIconProps}>
      <path d="M18 6 6 18" />
      <path d="m6 6 12 12" />
    </svg>
  ),
  chevronLeft: (
    <svg {...headerIconProps}>
      <path d="m14 18-6-6 6-6" />
    </svg>
  ),
  chevronRight: (
    <svg {...headerIconProps}>
      <path d="m10 6 6 6-6 6" />
    </svg>
  ),
  sun: (
    <svg {...headerIconProps}>
      <circle cx="12" cy="12" r="4" />
      <path d="M12 2v2" />
      <path d="M12 20v2" />
      <path d="m4.93 4.93 1.41 1.41" />
      <path d="m17.66 17.66 1.41 1.41" />
      <path d="M2 12h2" />
      <path d="M20 12h2" />
      <path d="m6.34 17.66-1.41 1.41" />
      <path d="m19.07 4.93-1.41 1.41" />
    </svg>
  ),
  moon: (
    <svg {...headerIconProps}>
      <path d="M12 3a6 6 0 0 0 9 9 9 9 0 1 1-9-9z" />
    </svg>
  ),
  whiteTheme: (
    <svg {...headerIconProps}>
      <circle cx="12" cy="12" r="7" />
      <circle cx="12" cy="12" r="3" fill="currentColor" stroke="none" />
    </svg>
  ),
  autoTheme: (
    <svg {...headerIconProps}>
      <defs>
        <clipPath id="mainLayoutAutoThemeSunLeftHalf">
          <rect x="0" y="0" width="12" height="24" />
        </clipPath>
      </defs>
      <circle cx="12" cy="12" r="4" />
      <circle
        cx="12"
        cy="12"
        r="4"
        clipPath="url(#mainLayoutAutoThemeSunLeftHalf)"
        fill="currentColor"
      />
      <path d="M12 2v2" />
      <path d="M12 20v2" />
      <path d="M4.93 4.93l1.41 1.41" />
      <path d="M17.66 17.66l1.41 1.41" />
      <path d="M2 12h2" />
      <path d="M20 12h2" />
      <path d="M6.34 17.66l-1.41 1.41" />
      <path d="M19.07 4.93l-1.41 1.41" />
    </svg>
  ),
};

const THEME_CARDS: Array<{
  key: Theme;
  labelKey: string;
  colors: { bg: string; card: string; border: string; text: string; textMuted: string };
}> = [
  {
    key: 'auto',
    labelKey: 'theme.auto',
    colors: {
      bg: 'linear-gradient(135deg, #ffffff 0 50%, #000000 50% 100%)',
      card: 'linear-gradient(135deg, #ffffff 0 50%, #000000 50% 100%)',
      border: '#bdbdbd',
      text: '#2d2a26',
      textMuted: 'linear-gradient(135deg, #6a645e 0 50%, #5a5a5a 50% 100%)',
    },
  },
  {
    key: 'white',
    labelKey: 'theme.white',
    colors: {
      bg: '#ffffff',
      card: '#ffffff',
      border: '#e5e5e5',
      text: '#2d2a26',
      textMuted: '#6a645e',
    },
  },
  {
    key: 'light',
    labelKey: 'theme.light',
    colors: {
      bg: '#faf9f5',
      card: '#f0eee8',
      border: '#e3e1db',
      text: '#2d2a26',
      textMuted: '#6a645e',
    },
  },
  {
    key: 'dark',
    labelKey: 'theme.dark',
    colors: {
      bg: '#000000',
      card: '#000000',
      border: '#2a2a2a',
      text: '#ededed',
      textMuted: '#808080',
    },
  },
];

export function MainLayout() {
  const { t } = useTranslation();
  const { showNotification } = useNotificationStore();
  const location = useLocation();

  const connectionStatus = useAuthStore((state) => state.connectionStatus);
  const apiBase = useAuthStore((state) => state.apiBase);
  const supportsPlugin = useAuthStore((state) => state.supportsPlugin);

  const fetchConfig = useConfigStore((state) => state.fetchConfig);
  const invalidateConfig = useConfigStore((state) => state.invalidate);

  const theme = useThemeStore((state) => state.theme);
  const setTheme = useThemeStore((state) => state.setTheme);

  const [sidebarOpen, setSidebarOpen] = useState(false);
  const [sidebarCollapsed, setSidebarCollapsed] = useState(false);
  const [authFilesCount, setAuthFilesCount] = useState<number | null>(null);
  const [railTooltip, setRailTooltip] = useState<{
    targetID: string;
    label: string;
    meta?: string;
    anchorTop: number;
    top: number;
  } | null>(null);
  const [themeMenuOpen, setThemeMenuOpen] = useState(false);
  const [refreshing, setRefreshing] = useState(false);
  const [pluginResources, setPluginResources] = useState<PluginResourceEntry[]>([]);
  const [expandedPluginResourceIDs, setExpandedPluginResourceIDs] = useState<Set<string>>(
    () => new Set()
  );
  const contentRef = useRef<HTMLDivElement | null>(null);
  const authFilesCountRequestRef = useRef(0);
  const railTooltipRef = useRef<HTMLDivElement | null>(null);
  const focusedRailItemRef = useRef<HTMLElement | null>(null);
  const themeMenuRef = useRef<HTMLDivElement | null>(null);
  const headerRef = useRef<HTMLElement | null>(null);
  const themeButtonRef = useRef<HTMLButtonElement | null>(null);
  const menuButtonRef = useRef<HTMLButtonElement | null>(null);
  const sidebarRef = useRef<HTMLElement | null>(null);
  const mainRef = useRef<HTMLElement | null>(null);
  const isMobile = useMediaQuery(`(max-width: ${MOBILE_BREAKPOINT_PX}px)`);
  const drawerOpen = isMobile && sidebarOpen;

  const brandName = t('title.main');
  // The app log tail sizes itself to the viewport, so its tab gets the fixed-height layout.
  const isLogsPage =
    location.pathname === ROUTES.requestLogs &&
    new URLSearchParams(location.search).get('tab') === 'app';
  const isPluginResourcePage = location.pathname.startsWith('/plugin-pages');
  const showSidebarLabels = !sidebarCollapsed || sidebarOpen;

  // Keep floating header height available to sticky mobile elements and overlays.
  useLayoutEffect(() => {
    const updateHeaderHeight = () => {
      const height = headerRef.current?.offsetHeight;
      if (height) {
        document.documentElement.style.setProperty('--header-height', `${height}px`);
      }
    };

    updateHeaderHeight();

    const resizeObserver =
      typeof ResizeObserver !== 'undefined' && headerRef.current
        ? new ResizeObserver(updateHeaderHeight)
        : null;
    if (resizeObserver && headerRef.current) {
      resizeObserver.observe(headerRef.current);
    }

    window.addEventListener('resize', updateHeaderHeight);

    return () => {
      if (resizeObserver) {
        resizeObserver.disconnect();
      }
      window.removeEventListener('resize', updateHeaderHeight);
    };
  }, []);

  useLayoutEffect(() => {
    if (!railTooltip) return;

    const updateRailTooltipPosition = () => {
      const tooltip = railTooltipRef.current;
      if (!tooltip) return;

      const halfHeight = tooltip.offsetHeight / 2;
      const minTop = NAV_TOOLTIP_VIEWPORT_MARGIN + halfHeight;
      const maxTop = Math.max(
        minTop,
        window.innerHeight - NAV_TOOLTIP_VIEWPORT_MARGIN - halfHeight
      );
      const top = Math.min(maxTop, Math.max(minTop, railTooltip.anchorTop));

      setRailTooltip((current) => {
        if (!current || current.targetID !== railTooltip.targetID || current.top === top) {
          return current;
        }
        return { ...current, top };
      });
    };

    updateRailTooltipPosition();
    window.addEventListener('resize', updateRailTooltipPosition);
    return () => window.removeEventListener('resize', updateRailTooltipPosition);
  }, [railTooltip]);

  // Keep the content center available to bottom overlays that align with the main area.
  useLayoutEffect(() => {
    const updateContentCenter = () => {
      const el = contentRef.current;
      if (!el) return;
      const rect = el.getBoundingClientRect();
      const centerX = rect.left + rect.width / 2;
      document.documentElement.style.setProperty('--content-center-x', `${centerX}px`);
    };

    updateContentCenter();

    const resizeObserver =
      typeof ResizeObserver !== 'undefined' && contentRef.current
        ? new ResizeObserver(updateContentCenter)
        : null;

    if (resizeObserver && contentRef.current) {
      resizeObserver.observe(contentRef.current);
    }

    window.addEventListener('resize', updateContentCenter);

    return () => {
      if (resizeObserver) {
        resizeObserver.disconnect();
      }
      window.removeEventListener('resize', updateContentCenter);
      document.documentElement.style.removeProperty('--content-center-x');
    };
  }, []);

  const closeThemeMenu = useCallback((returnFocus: boolean) => {
    setThemeMenuOpen(false);
    if (returnFocus) themeButtonRef.current?.focus();
  }, []);
  useMenuDismiss(themeMenuOpen, themeMenuRef, closeThemeMenu);

  // The open drawer is modal: focus moves into it, Tab stays inside it and the menu button that
  // closes it, Escape closes it and returns focus, and the page behind does not scroll.
  useEffect(() => {
    if (!drawerOpen) return;
    lockScroll();
    const focusables = () => [
      ...(menuButtonRef.current ? [menuButtonRef.current] : []),
      ...Array.from(sidebarRef.current?.querySelectorAll<HTMLElement>(FOCUSABLE_SELECTOR) ?? []),
    ];
    sidebarRef.current?.querySelector<HTMLElement>(FOCUSABLE_SELECTOR)?.focus();

    const handleKeyDown = (event: KeyboardEvent) => {
      if (hasOpenLayer()) return;
      if (event.key === 'Escape') {
        event.preventDefault();
        setSidebarOpen(false);
        menuButtonRef.current?.focus();
        return;
      }
      if (event.key !== 'Tab') return;
      // The menu button and the drawer are not adjacent in the DOM, so step through them here.
      const items = focusables();
      if (items.length === 0) return;
      event.preventDefault();
      const index = items.indexOf(document.activeElement as HTMLElement);
      const step = event.shiftKey ? -1 : 1;
      const next = index === -1 ? (event.shiftKey ? items.length - 1 : 0) : index + step;
      items[(next + items.length) % items.length].focus();
    };
    document.addEventListener('keydown', handleKeyDown);
    return () => {
      document.removeEventListener('keydown', handleKeyDown);
      unlockScroll();
    };
  }, [drawerOpen]);

  const toggleThemeMenu = useCallback(() => {
    setThemeMenuOpen((prev) => !prev);
  }, []);

  const handleThemeSelect = useCallback(
    (nextTheme: Theme) => {
      setTheme(nextTheme);
      setThemeMenuOpen(false);
    },
    [setTheme]
  );

  const loadPluginResources = useCallback(async () => {
    if (connectionStatus !== 'connected' || !supportsPlugin) {
      setPluginResources([]);
      return;
    }

    try {
      const plugins = await pluginsApi.list();
      setPluginResources(collectPluginResourceEntries(plugins.plugins));
    } catch {
      setPluginResources([]);
    }
  }, [connectionStatus, supportsPlugin]);

  const loadAuthFilesCount = useCallback(async () => {
    const requestID = ++authFilesCountRequestRef.current;
    if (connectionStatus !== 'connected') {
      setAuthFilesCount(null);
      return;
    }

    try {
      const response = await authFilesApi.list();
      if (requestID !== authFilesCountRequestRef.current) return;
      setAuthFilesCount(Array.isArray(response?.files) ? response.files.length : null);
    } catch {
      if (requestID !== authFilesCountRequestRef.current) return;
      setAuthFilesCount(null);
    }
  }, [connectionStatus]);

  useEffect(() => {
    const timer = window.setTimeout(() => {
      void loadPluginResources();
      void loadAuthFilesCount();
    }, 0);

    window.addEventListener(PLUGIN_RESOURCES_REFRESH_EVENT, loadPluginResources);
    window.addEventListener(AUTH_FILES_CHANGED_EVENT, loadAuthFilesCount);

    return () => {
      authFilesCountRequestRef.current += 1;
      window.clearTimeout(timer);
      window.removeEventListener(PLUGIN_RESOURCES_REFRESH_EVENT, loadPluginResources);
      window.removeEventListener(AUTH_FILES_CHANGED_EVENT, loadAuthFilesCount);
    };
  }, [apiBase, loadPluginResources, loadAuthFilesCount]);

  const pluginResourceGroups = pluginResources.reduce<
    Array<{ pluginID: string; pluginTitle: string; entries: PluginResourceEntry[] }>
  >((groups, resource) => {
    const group = groups.find((item) => item.pluginID === resource.pluginID);
    if (group) {
      group.entries.push(resource);
      return groups;
    }

    groups.push({
      pluginID: resource.pluginID,
      pluginTitle: resource.pluginTitle,
      entries: [resource],
    });
    return groups;
  }, []);

  const pluginPageNavItems: SidebarNavItem[] = supportsPlugin
    ? pluginResourceGroups.flatMap((group): SidebarNavItem[] => {
        if (group.entries.length === 1) {
          const resource = group.entries[0];
          const pluginLogo = resolvePluginAssetURL(resource.pluginLogo, apiBase);
          return [
            {
              path: resource.route,
              label: resource.label,
              meta: resource.description,
              icon: <PluginSidebarIcon src={pluginLogo} />,
            },
          ];
        }

        const pluginLogo = resolvePluginAssetURL(group.entries[0]?.pluginLogo ?? '', apiBase);
        return [
          {
            kind: 'drawer',
            id: `plugin-pages-${group.pluginID}`,
            label: group.pluginTitle,
            meta: t('plugin_resource.page_count', { count: group.entries.length }),
            icon: <PluginSidebarIcon src={pluginLogo} />,
            children: group.entries.map((resource) => ({
              path: resource.route,
              label: resource.label,
              meta: resource.description,
              icon: <span className="nav-sub-dot" aria-hidden="true" />,
            })),
          },
        ];
      })
    : [];

  const link = (key: keyof typeof sidebarIcons, labelKey: string): SidebarNavLinkItem => ({
    path: ROUTES[key],
    labelKey,
    icon: sidebarIcons[key],
  });

  const navGroups: SidebarNavGroup[] = [
    {
      id: 'observe',
      labelKey: 'nav_groups.observe',
      items: [
        link('dashboard', 'nav.dashboard'),
        {
          ...link('accounts', 'nav.accounts'),
          badge: authFilesCount ?? undefined,
          badgeLabel:
            typeof authFilesCount === 'number'
              ? t('sidebar.accounts_count', { count: authFilesCount })
              : undefined,
        },
        link('usage', 'nav.usage'),
        link('requestLogs', 'nav.request_logs'),
      ],
    },
    {
      id: 'server',
      labelKey: 'nav_groups.server',
      items: [
        link('routing', 'nav.routing'),
        link('config', 'nav.config_management'),
        ...(supportsPlugin
          ? [link('plugins', 'nav.plugins'), link('pluginStore', 'nav.plugin_store')]
          : []),
        link('backups', 'nav.backups'),
        link('access', 'nav.access'),
        link('settings', 'nav.settings'),
        link('system', 'nav.system_info'),
      ],
    },
    ...(pluginPageNavItems.length > 0
      ? [
          {
            id: 'plugin-pages',
            labelKey: 'nav_groups.plugin_pages',
            items: pluginPageNavItems,
          },
        ]
      : []),
  ];
  const navItems = navGroups.flatMap((group) => flattenNavItems(group.items));
  const navOrder = navItems.map((item) => item.path);
  const getRouteOrder = (pathname: string) => {
    const trimmedPath =
      pathname.length > 1 && pathname.endsWith('/') ? pathname.slice(0, -1) : pathname;
    const normalizedPath = trimmedPath === '/dashboard' ? '/' : trimmedPath;

    const routingIndex = navOrder.indexOf(ROUTES.routing);
    if (routingIndex !== -1) {
      if (normalizedPath === ROUTES.oauthExcluded) return routingIndex + 0.1;
      if (normalizedPath === ROUTES.oauthModelAlias) return routingIndex + 0.2;
    }

    const exactIndex = navOrder.indexOf(normalizedPath);
    if (exactIndex !== -1) return exactIndex;
    const nestedIndex = navOrder.findIndex(
      (path) => path !== '/' && normalizedPath.startsWith(`${path}/`)
    );
    return nestedIndex === -1 ? null : nestedIndex;
  };

  const routeOrder = getRouteOrder(location.pathname);
  const currentNavItem = routeOrder === null ? undefined : navItems[Math.floor(routeOrder)];
  const pageLabel = currentNavItem
    ? (currentNavItem.label ?? (currentNavItem.labelKey ? t(currentNavItem.labelKey) : ''))
    : '';
  const pageTitle = pageLabel ? t('shell.page_title', { page: pageLabel }) : brandName;

  useEffect(() => {
    document.title = pageTitle;
  }, [pageTitle]);

  // After a page change, focus the new page so keyboard and screen-reader users land on it rather
  // than on the link they left (which the closing drawer also makes inert), and announce it.
  const [pageAnnouncement, setPageAnnouncement] = useState('');
  const announcedPathRef = useRef(location.pathname);
  useEffect(() => {
    if (announcedPathRef.current === location.pathname) return;
    announcedPathRef.current = location.pathname;
    if (!hasOpenLayer()) mainRef.current?.focus({ preventScroll: true });
    setPageAnnouncement(pageTitle);
  }, [location.pathname, pageTitle]);

  const getTransitionVariant = useCallback((fromPathname: string, toPathname: string) => {
    const normalize = (pathname: string) => {
      const trimmed =
        pathname.length > 1 && pathname.endsWith('/') ? pathname.slice(0, -1) : pathname;
      return trimmed === '/dashboard' ? '/' : trimmed;
    };

    const from = normalize(fromPathname);
    const to = normalize(toPathname);
    const isRouting = (pathname: string) =>
      pathname === ROUTES.routing || pathname.startsWith(`${ROUTES.routing}/`);
    if (isRouting(from) && isRouting(to)) return 'ios';
    return 'vertical';
  }, []);

  const handleRefreshAll = async () => {
    if (refreshing) return;
    setRefreshing(true);
    try {
      invalidateConfig();
      const [page, shared] = await Promise.all([
        triggerHeaderRefresh(),
        Promise.allSettled([fetchConfig(true), loadPluginResources(), loadAuthFilesCount()]),
      ]);
      const report = summarizeRefreshAll(shared, page);
      if (report?.kind === 'failed') {
        showNotification(
          `${t('notification.refresh_failed')}${report.message ? `: ${report.message}` : ''}`,
          'error'
        );
      } else if (report?.kind === 'success') {
        showNotification(t('notification.data_refreshed'), 'success');
      }
    } finally {
      setRefreshing(false);
    }
  };

  const togglePluginResourceDrawer = useCallback((drawerID: string) => {
    setExpandedPluginResourceIDs((current) => {
      const next = new Set(current);
      if (next.has(drawerID)) {
        next.delete(drawerID);
      } else {
        next.add(drawerID);
      }
      return next;
    });
  }, []);

  const showRailTooltip = useCallback(
    (event: SyntheticEvent<HTMLElement>, targetID: string, label: string, meta?: string) => {
      const rect = event.currentTarget.getBoundingClientRect();
      const anchorTop = rect.top + rect.height / 2;
      setRailTooltip({ targetID, label, meta, anchorTop, top: anchorTop });
    },
    []
  );
  const hideRailTooltip = useCallback(() => setRailTooltip(null), []);
  const handleRailTooltipMouseEnter = useCallback(
    (event: ReactMouseEvent<HTMLElement>, targetID: string, label: string, meta?: string) => {
      const focusedItem = focusedRailItemRef.current;
      if (focusedItem && focusedItem !== event.currentTarget) return;
      showRailTooltip(event, targetID, label, meta);
    },
    [showRailTooltip]
  );
  const handleRailTooltipMouseLeave = useCallback(() => {
    if (!focusedRailItemRef.current) hideRailTooltip();
  }, [hideRailTooltip]);
  const handleRailTooltipFocus = useCallback(
    (event: SyntheticEvent<HTMLElement>, targetID: string, label: string, meta?: string) => {
      focusedRailItemRef.current = event.currentTarget;
      showRailTooltip(event, targetID, label, meta);
    },
    [showRailTooltip]
  );
  const handleRailTooltipBlur = useCallback(
    (event: SyntheticEvent<HTMLElement>, targetID: string, label: string, meta?: string) => {
      if (focusedRailItemRef.current === event.currentTarget) {
        focusedRailItemRef.current = null;
      }
      if (event.currentTarget.matches(':hover')) {
        showRailTooltip(event, targetID, label, meta);
      } else {
        hideRailTooltip();
      }
    },
    [hideRailTooltip, showRailTooltip]
  );

  const isMac = useMemo(() => {
    if (typeof navigator === 'undefined') return false;
    const platform =
      (navigator as unknown as { userAgentData?: { platform?: string } }).userAgentData?.platform ||
      navigator.platform ||
      navigator.userAgent ||
      '';
    return /(Mac|iPhone|iPod|iPad)/i.test(platform);
  }, []);

  const shortcutText = getSidebarShortcutLabel(isMac);

  useEffect(() => {
    const handleKeyDown = (event: KeyboardEvent) => {
      if (isSidebarToggleShortcut(event) && !hasOpenLayer()) {
        event.preventDefault();
        hideRailTooltip();
        setSidebarCollapsed((prev) => !prev);
      }
    };

    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [hideRailTooltip]);

  const renderNavBadge = (badge?: number, badgeLabel?: string) =>
    typeof badge === 'number' ? (
      <>
        {badge > 0 ? (
          <span className="nav-badge" aria-hidden="true">
            {badge}
          </span>
        ) : null}
        {badgeLabel ? <span className="nav-badge-sr-only">{badgeLabel}</span> : null}
      </>
    ) : null;

  const renderNavLink = (item: SidebarNavLinkItem, className = 'nav-item') => {
    const itemLabel = item.label ?? (item.labelKey ? t(item.labelKey) : '');
    const itemMeta = item.meta ?? (item.metaKey ? t(item.metaKey) : '');
    const accessibleLabel = item.badgeLabel ? `${itemLabel}, ${item.badgeLabel}` : itemLabel;

    return (
      <NavLink
        key={item.path}
        to={item.path}
        className={({ isActive }) => `${className} ${isActive ? 'active' : ''}`}
        onClick={() => {
          focusedRailItemRef.current = null;
          setSidebarOpen(false);
          hideRailTooltip();
        }}
        aria-label={showSidebarLabels ? undefined : accessibleLabel}
        aria-describedby={
          !showSidebarLabels && itemMeta && railTooltip?.targetID === item.path
            ? NAV_TOOLTIP_ID
            : undefined
        }
        onMouseEnter={
          showSidebarLabels
            ? undefined
            : (event) => handleRailTooltipMouseEnter(event, item.path, itemLabel, itemMeta)
        }
        onMouseLeave={showSidebarLabels ? undefined : handleRailTooltipMouseLeave}
        onFocus={
          showSidebarLabels
            ? undefined
            : (event) => handleRailTooltipFocus(event, item.path, itemLabel, itemMeta)
        }
        onBlur={
          showSidebarLabels
            ? undefined
            : (event) => handleRailTooltipBlur(event, item.path, itemLabel, itemMeta)
        }
      >
        <span className="nav-icon">{item.icon}</span>
        {showSidebarLabels ? (
          <>
            <span className="nav-text">
              <span className="nav-label">{itemLabel}</span>
            </span>
            {renderNavBadge(item.badge, item.badgeLabel)}
          </>
        ) : (
          renderNavBadge(item.badge)
        )}
      </NavLink>
    );
  };

  const renderNavItem = (item: SidebarNavItem) => {
    if (item.kind !== 'drawer') {
      return renderNavLink(item);
    }

    const isActive = item.children.some((child) => child.path === location.pathname);
    const isOpen = isActive || expandedPluginResourceIDs.has(item.id);

    return (
      <div className={`nav-drawer ${isOpen ? 'open' : ''}`} key={item.id}>
        <button
          type="button"
          className={`nav-item nav-drawer-toggle ${isActive ? 'active' : ''} ${
            isOpen ? 'open' : ''
          }`}
          onClick={() => togglePluginResourceDrawer(item.id)}
          aria-label={showSidebarLabels ? undefined : item.label}
          aria-describedby={
            !showSidebarLabels && item.meta && railTooltip?.targetID === item.id
              ? NAV_TOOLTIP_ID
              : undefined
          }
          aria-expanded={isOpen}
          onMouseEnter={
            showSidebarLabels
              ? undefined
              : (event) => handleRailTooltipMouseEnter(event, item.id, item.label, item.meta)
          }
          onMouseLeave={showSidebarLabels ? undefined : handleRailTooltipMouseLeave}
          onFocus={
            showSidebarLabels
              ? undefined
              : (event) => handleRailTooltipFocus(event, item.id, item.label, item.meta)
          }
          onBlur={
            showSidebarLabels
              ? undefined
              : (event) => handleRailTooltipBlur(event, item.id, item.label, item.meta)
          }
        >
          <span className="nav-icon">{item.icon}</span>
          {showSidebarLabels && (
            <>
              <span className="nav-text">
                <span className="nav-label">{item.label}</span>
              </span>
              <span className="nav-drawer-caret" aria-hidden="true">
                <IconChevronDown size={14} />
              </span>
            </>
          )}
        </button>
        {isOpen ? (
          <div className="nav-sub-list">
            {item.children.map((child) => renderNavLink(child, 'nav-item nav-sub-item'))}
          </div>
        ) : null}
      </div>
    );
  };

  const mobileSidebarToggleLabel = sidebarOpen
    ? t('sidebar.close_navigation')
    : t('sidebar.open_navigation');

  const sidebarToggleLabel = sidebarCollapsed ? t('sidebar.expand') : t('sidebar.collapse');

  return (
    <div
      className={`app-shell ${sidebarCollapsed ? 'sidebar-is-collapsed' : ''} ${
        isPluginResourcePage ? 'plugin-resource-shell' : ''
      }`}
    >
      <a
        className="skip-link"
        href={`#${MAIN_CONTENT_ID}`}
        onClick={(event) => {
          event.preventDefault();
          mainRef.current?.focus();
        }}
      >
        {t('shell.skip_to_content')}
      </a>
      <div className="visually-hidden" aria-live="polite">
        {pageAnnouncement}
      </div>
      <div className="top-gradient-blur" aria-hidden="true" />

      <header className="main-header" ref={headerRef}>
        <button
          type="button"
          className="sidebar-toggle-floating"
          onClick={() => {
            hideRailTooltip();
            setSidebarCollapsed((prev) => !prev);
          }}
          onMouseEnter={(event) =>
            handleRailTooltipMouseEnter(event, 'sidebar-toggle', sidebarToggleLabel, shortcutText)
          }
          onMouseLeave={handleRailTooltipMouseLeave}
          onFocus={(event) =>
            handleRailTooltipFocus(event, 'sidebar-toggle', sidebarToggleLabel, shortcutText)
          }
          onBlur={(event) =>
            handleRailTooltipBlur(event, 'sidebar-toggle', sidebarToggleLabel, shortcutText)
          }
          aria-label={`${sidebarToggleLabel} (${shortcutText})`}
          aria-describedby={railTooltip?.targetID === 'sidebar-toggle' ? NAV_TOOLTIP_ID : undefined}
        >
          {sidebarCollapsed ? headerIcons.chevronRight : headerIcons.chevronLeft}
        </button>

        <div className="mobile-sidebar-actions">
          <Button
            ref={menuButtonRef}
            className="mobile-menu-btn"
            variant="ghost"
            size="sm"
            onClick={() => setSidebarOpen((prev) => !prev)}
            title={mobileSidebarToggleLabel}
            aria-label={mobileSidebarToggleLabel}
            aria-expanded={sidebarOpen}
            aria-controls={SIDEBAR_ID}
          >
            {sidebarOpen ? headerIcons.close : headerIcons.menu}
          </Button>
        </div>

        <div className="header-actions floating-actions">
          <Button
            className={refreshing ? 'header-refreshing' : undefined}
            variant="ghost"
            size="sm"
            onClick={handleRefreshAll}
            title={t('header.refresh_all')}
            aria-label={t('header.refresh_all')}
            aria-disabled={refreshing}
          >
            {headerIcons.refresh}
          </Button>
          <div className={`theme-menu ${themeMenuOpen ? 'open' : ''}`} ref={themeMenuRef}>
            <Button
              ref={themeButtonRef}
              variant="ghost"
              size="sm"
              onClick={toggleThemeMenu}
              title={t('theme.switch')}
              aria-label={t('theme.switch')}
              aria-expanded={themeMenuOpen}
              aria-controls={themeMenuOpen ? THEME_MENU_ID : undefined}
            >
              {theme === 'auto'
                ? headerIcons.autoTheme
                : theme === 'dark'
                  ? headerIcons.moon
                  : theme === 'white'
                    ? headerIcons.whiteTheme
                    : headerIcons.sun}
            </Button>
            {themeMenuOpen && (
              <div
                id={THEME_MENU_ID}
                className="notification entering theme-menu-popover"
                role="group"
                aria-label={t('theme.switch')}
              >
                {THEME_CARDS.map((tc) => (
                  <button
                    key={tc.key}
                    type="button"
                    className={`theme-card ${theme === tc.key ? 'active' : ''}`}
                    onClick={() => handleThemeSelect(tc.key)}
                    aria-pressed={theme === tc.key}
                  >
                    <div
                      className="theme-card-preview"
                      style={{
                        background: tc.colors.bg,
                        border: `1px solid ${tc.colors.border}`,
                      }}
                    >
                      <div
                        className="theme-card-header"
                        style={{
                          background: tc.colors.card,
                          borderBottom: `1px solid ${tc.colors.border}`,
                        }}
                      />
                      <div className="theme-card-body">
                        <div
                          className="theme-card-sidebar"
                          style={{
                            background: tc.colors.card,
                            borderRight: `1px solid ${tc.colors.border}`,
                          }}
                        />
                        <div className="theme-card-content" style={{ background: tc.colors.bg }}>
                          <div
                            className="theme-card-line"
                            style={{ background: tc.colors.textMuted }}
                          />
                          <div
                            className="theme-card-line short"
                            style={{ background: tc.colors.textMuted }}
                          />
                        </div>
                      </div>
                    </div>
                    <span className="theme-card-label">{t(tc.labelKey)}</span>
                  </button>
                ))}
              </div>
            )}
          </div>
        </div>
      </header>

      <div className="main-body">
        <div
          className={`sidebar-backdrop ${sidebarOpen ? 'visible' : ''}`}
          onClick={() => setSidebarOpen(false)}
          aria-hidden="true"
        />

        <aside
          id={SIDEBAR_ID}
          ref={sidebarRef}
          className={`sidebar ${sidebarOpen ? 'open' : ''} ${sidebarCollapsed ? 'collapsed' : ''}`}
          inert={isMobile && !sidebarOpen}
        >
          <div className="sidebar-header">
            <div className="sidebar-brand" title={brandName}>
              <TaprootMark className="sidebar-brand-logo" />
              {showSidebarLabels && (
                <span className="sidebar-brand-text">
                  <span className="sidebar-brand-title">{brandName}</span>
                  <span className="sidebar-brand-subtitle">{t('sidebar.subtitle')}</span>
                </span>
              )}
            </div>
          </div>

          <nav className="nav-section" aria-label={t('shell.main_navigation')}>
            {navGroups.map((group, idx) => (
              <div className="nav-group" key={group.id}>
                {showSidebarLabels ? (
                  <div className="nav-group-label">{t(group.labelKey)}</div>
                ) : (
                  idx > 0 && <div className="nav-group-divider" aria-hidden="true" />
                )}
                {group.items.map((item) => renderNavItem(item))}
              </div>
            ))}
          </nav>
        </aside>

        {railTooltip && (
          <div
            ref={railTooltipRef}
            id={NAV_TOOLTIP_ID}
            className="nav-tooltip"
            role="tooltip"
            style={{ top: railTooltip.top }}
          >
            <span className="nav-tooltip-label" aria-hidden="true">
              {railTooltip.label}
            </span>
            {railTooltip.meta ? <span className="nav-tooltip-meta">{railTooltip.meta}</span> : null}
          </div>
        )}

        <div
          className={`content${isLogsPage ? ' content-logs' : ''}${
            isPluginResourcePage ? ' content-plugin-resource' : ''
          }`}
          ref={contentRef}
        >
          <main
            id={MAIN_CONTENT_ID}
            ref={mainRef}
            tabIndex={-1}
            className={`main-content${isLogsPage ? ' main-content-logs' : ''}${
              isPluginResourcePage ? ' main-content-plugin-resource' : ''
            }`}
          >
            <PageTransition
              render={(location) => <MainRoutes location={location} />}
              getRouteOrder={getRouteOrder}
              getTransitionVariant={getTransitionVariant}
              scrollContainerRef={contentRef}
            />
          </main>
        </div>
      </div>
    </div>
  );
}
