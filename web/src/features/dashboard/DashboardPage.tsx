import { useMemo } from 'react';
import { Link } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import {
  IconSidebarAccounts,
  IconSidebarConfig,
  IconSidebarLogs,
  IconSidebarSystem,
} from '@/components/ui/icons';
import { useAuthStore } from '@/stores';
import { ROUTES } from '@/router/routes';
import { useHeaderRefresh } from '@/hooks/useHeaderRefresh';
import { useEmailMask } from '@/hooks/useEmailMask';
import { formatCompactNumber, formatDateValue, formatPercent } from '@/utils/format';
import { DashboardUsageSummary } from '@/features/taproot-usage/components/DashboardUsageSummary';
import { useDashboardOverview, type ReadStatus } from './hooks/useDashboardOverview';
import { LiveWire } from './components/LiveWire';
import { Meter } from './components/Meter';
import { Sparkline } from './components/Sparkline';
import { ThroughputChart } from './components/ThroughputChart';
import { useCountUp, useRevealGroup, useRevealOnScroll } from '@/hooks/motion';
import { providerLabel, splitWindowMinutes, toneForSuccessRate, type MeterTone } from './utils';
import { credentialMeter, dashboardVerdict, redactProxyUrl, versionLabel } from './model';
import styles from './dashboard.module.scss';

const DASH = '—';

/** KPI 卡左上角色签：有语义色调的卡用状态色，其余保持中性 */
const TILE_ACCENTS: Record<MeterTone, string> = {
  good: 'var(--viz-success)',
  warning: 'var(--amber-color)',
  critical: 'var(--viz-failure)',
  idle: 'var(--text-tertiary)',
};

const clockFormat = new Intl.DateTimeFormat(undefined, {
  hour: '2-digit',
  minute: '2-digit',
  second: '2-digit',
});

/** 大数字：六位以内用千分位，再往上压缩，避免撑破排版 */
const formatHeadline = (value: number): string =>
  value < 100_000 ? value.toLocaleString() : formatCompactNumber(value);

export function DashboardPage() {
  const { t, i18n } = useTranslation();
  const { mask } = useEmailMask();
  const serverVersion = useAuthStore((state) => state.serverVersion);
  const serverBuildDate = useAuthStore((state) => state.serverBuildDate);

  const {
    connectionStatus,
    connected,
    config,
    traffic,
    trafficAtMs,
    providers,
    credentials,
    models,
    errors,
    status,
    today,
    quota,
    nowMs,
    refresh,
  } = useDashboardOverview();
  const trafficKnown = trafficAtMs !== null;

  useHeaderRefresh(refresh, connected);

  /* Hero 与静态网格走分组级联；异步内容区（图表/供应商）保持整块 reveal */
  const heroRef = useRevealGroup<HTMLElement>();
  const statsRef = useRevealGroup<HTMLElement>(0.12);
  const trafficRef = useRevealOnScroll<HTMLElement>();
  const fleetRef = useRevealOnScroll<HTMLElement>();
  const detailRef = useRevealGroup<HTMLElement>();
  const ctaRef = useRevealGroup<HTMLElement>();

  const animatedTotal = useCountUp(traffic.total, connected);

  const windowLabel = useMemo(() => {
    const { hours, minutes } = splitWindowMinutes(traffic.windowMinutes);
    if (hours === 0) return t('dashboard.window_m', { minutes });
    if (minutes === 0) return t('dashboard.window_h', { hours });
    return t('dashboard.window_hm', { hours, minutes });
  }, [traffic.windowMinutes, t]);

  const heroSparkPoints = useMemo(
    () => traffic.buckets.map((bucket) => bucket.success + bucket.failed),
    [traffic.buckets]
  );

  const routingStrategy = useMemo(() => {
    const raw = config?.routingStrategy?.trim() ?? '';
    if (!raw) return DASH;
    if (raw === 'round-robin') return t('basic_settings.routing_strategy_round_robin');
    if (raw === 'weighted-round-robin') {
      return t('basic_settings.routing_strategy_weighted_round_robin');
    }
    if (raw === 'fill-first') return t('basic_settings.routing_strategy_fill_first');
    if (raw === 'soonest-reset-first') {
      return t('config_management.visual.sections.network.strategy_soonest_reset_first');
    }
    return raw;
  }, [config?.routingStrategy, t]);

  const unknownProviderLabel = t('dashboard.provider_unknown');
  const successRateTone = toneForSuccessRate(traffic.successRate);

  const verdict = dashboardVerdict({
    connectionStatus,
    loading: status.recent === 'loading',
    traffic: trafficKnown ? traffic : null,
    credentials,
  });
  const credentialsMeter = credentialMeter(credentials);

  /* 句号状态灯只在「有活着的流量」时呼吸；离线/静默时保持安静 */
  const heroAlive = connected && traffic.total > 0;

  const connectionLabel = t(
    connectionStatus === 'connected'
      ? 'common.connected'
      : connectionStatus === 'connecting'
        ? 'common.connecting'
        : 'common.disconnected'
  );
  const heroMetaLine = [versionLabel(serverVersion), connectionLabel].filter(Boolean).join(' · ');
  const proxyUrl = config?.proxyUrl?.trim() ? redactProxyUrl(config.proxyUrl.trim()) : null;
  const loadErrors: string[] = [];
  if (errors.recent) {
    loadErrors.push(t('dashboard.load_failed_traffic', { message: errors.recent }));
  }
  if (errors.authFiles) {
    loadErrors.push(t('dashboard.load_failed_credentials', { message: errors.authFiles }));
  }

  const unreadHint = (readState: ReadStatus) =>
    readState === 'failed' ? t('dashboard.stat_read_failed') : t('common.loading');

  const statTiles = [
    {
      key: 'success',
      label: t('dashboard.success_rate'),
      value: traffic.successRate === null ? DASH : formatPercent(traffic.successRate),
      hint: trafficKnown
        ? t('dashboard.stat_success_hint', { count: traffic.total, window: windowLabel })
        : unreadHint(status.recent),
      meter: traffic.successRate,
      tone: successRateTone,
    },
    {
      key: 'credentials',
      label: t('dashboard.stat_credentials'),
      value: credentials ? credentials.total.toLocaleString() : DASH,
      hint: credentials
        ? credentials.total > 0
          ? t('dashboard.stat_credentials_hint', {
              active: credentials.active,
              disabled: credentials.disabled,
              unavailable: credentials.unavailable,
            })
          : t('dashboard.stat_credentials_empty')
        : unreadHint(status.authFiles),
      meter: credentialsMeter.value,
      tone: credentialsMeter.tone,
    },
    {
      key: 'models',
      label: t('dashboard.stat_models'),
      value: models.count === null ? DASH : models.count.toLocaleString(),
      hint: models.error
        ? mask(t('dashboard.stat_models_error', { message: models.error }))
        : t('dashboard.stat_models_hint'),
      meter: null,
      tone: undefined,
    },
  ];

  const runtimeRows: Array<{ label: string; value: string; mono?: boolean }> = [
    { label: t('dashboard.runtime_routing'), value: routingStrategy },
    {
      label: t('dashboard.runtime_retry'),
      // The config document leaves out a zero, and an unset request-retry is 0.
      value: config ? String(config.requestRetry ?? 0) : DASH,
    },
    { label: t('dashboard.runtime_version'), value: serverVersion?.trim() || DASH },
    {
      label: t('dashboard.runtime_build'),
      value: formatDateValue(serverBuildDate, i18n.language) || DASH,
    },
    {
      label: t('dashboard.runtime_proxy'),
      value: proxyUrl
        ? proxyUrl.redacted
          ? t('dashboard.runtime_proxy_redacted', { url: proxyUrl.value })
          : proxyUrl.value
        : DASH,
      mono: true,
    },
  ];

  const runtimeToggles = config
    ? [
        { label: t('dashboard.runtime_debug'), on: Boolean(config.debug) },
        { label: t('dashboard.runtime_file_logging'), on: Boolean(config.loggingToFile) },
        { label: t('dashboard.runtime_request_log'), on: Boolean(config.requestLog) },
        { label: t('dashboard.runtime_ws_auth'), on: Boolean(config.wsAuth) },
        { label: t('dashboard.runtime_model_prefix'), on: Boolean(config.forceModelPrefix) },
      ]
    : [];

  const ctaCards = [
    {
      to: ROUTES.accounts,
      icon: <IconSidebarAccounts size={20} />,
      title: t('nav.accounts'),
      description: t('dashboard.cta_accounts_desc'),
    },
    {
      to: ROUTES.config,
      icon: <IconSidebarConfig size={20} />,
      title: t('nav.config_management'),
      description: t('dashboard.cta_config_desc'),
    },
    {
      to: ROUTES.requestLogs,
      icon: <IconSidebarLogs size={20} />,
      title: t('nav.request_logs'),
      description: t('dashboard.cta_logs_desc'),
    },
    {
      to: ROUTES.system,
      icon: <IconSidebarSystem size={20} />,
      title: t('nav.system_info'),
      description: t('dashboard.cta_system_desc'),
    },
  ];

  return (
    <div className={styles.page}>
      <div className={styles.ambient} aria-hidden="true">
        <span className={styles.washTop} />
        <span className={styles.gridWash} />
      </div>

      {loadErrors.length > 0 && (
        <div className={styles.loadError} role="alert">
          {loadErrors.map((message) => (
            <p key={message}>{mask(message)}</p>
          ))}
          <p>{t('dashboard.load_failed_hint')}</p>
        </div>
      )}

      {/* ---------- Hero ---------- */}
      <section className={styles.hero} ref={heroRef}>
        <div className={styles.heroCopy}>
          <h1 className={styles.heroTitle} data-reveal>
            {t(`dashboard.${verdict.key}`)}
            <span
              className={`${styles.heroPeriod} ${heroAlive ? styles.heroPeriodLive : ''}`}
              style={{ color: TILE_ACCENTS[verdict.tone] }}
            >
              {t('dashboard.hero_period')}
            </span>
          </h1>
          <p className={styles.heroMeta} data-reveal>
            {heroMetaLine}
          </p>
          <div className={styles.heroActions} data-reveal>
            <Link to={ROUTES.accounts} className={styles.primaryAction}>
              {t('dashboard.cta_manage_credentials')}
            </Link>
            <Link to={ROUTES.requestLogs} className={styles.ghostAction}>
              {t('dashboard.cta_inspect_logs')}{' '}
              <span className={styles.linkArrow} aria-hidden="true">
                →
              </span>
            </Link>
          </div>
        </div>

        <div className={styles.heroPanel} data-reveal="scale">
          <div className={styles.heroPanelTop}>
            <span className={styles.heroPanelLabel}>{t('dashboard.hero_requests_label')}</span>
            {connected &&
              (status.recent === 'loading' ? (
                <span className={`${styles.liveBadge} ${styles.liveBadgeStale}`}>
                  {t('common.loading')}
                </span>
              ) : errors.recent ? (
                <span className={`${styles.liveBadge} ${styles.liveBadgeStale}`}>
                  {t('dashboard.hero_stale')}
                </span>
              ) : (
                <span className={styles.liveBadge}>
                  <i className={styles.liveDot} aria-hidden="true" />
                  {t('dashboard.hero_live')}
                </span>
              ))}
          </div>
          <strong className={styles.heroFigure}>
            {connected && trafficKnown ? formatHeadline(animatedTotal) : DASH}
          </strong>
          <span className={styles.heroPanelMeta}>
            {trafficAtMs === null
              ? t('dashboard.hero_window_meta', { window: windowLabel })
              : t('dashboard.hero_window_meta_updated', {
                  window: windowLabel,
                  time: clockFormat.format(trafficAtMs),
                })}
          </span>
          {traffic.total > 0 && (
            <div className={styles.ratioBar} aria-hidden="true">
              {traffic.totalSuccess > 0 && (
                <span
                  className={`${styles.ratioSegment} ${styles.splitSuccess}`}
                  style={{ flexGrow: traffic.totalSuccess }}
                />
              )}
              {traffic.totalFailure > 0 && (
                <span
                  className={`${styles.ratioSegment} ${styles.splitFailure}`}
                  style={{ flexGrow: traffic.totalFailure }}
                />
              )}
            </div>
          )}
          <div className={styles.heroSplit}>
            <span className={styles.heroSplitItem}>
              <i className={`${styles.splitSwatch} ${styles.splitSuccess}`} aria-hidden="true" />
              {t('stats.success')}
              <b>{traffic.totalSuccess.toLocaleString()}</b>
            </span>
            <span className={styles.heroSplitItem}>
              <i className={`${styles.splitSwatch} ${styles.splitFailure}`} aria-hidden="true" />
              {t('stats.failure')}
              <b>{traffic.totalFailure.toLocaleString()}</b>
            </span>
          </div>
        </div>

        <div className={styles.heroWire}>
          <LiveWire
            points={heroSparkPoints}
            color={TILE_ACCENTS[successRateTone === 'idle' ? 'good' : successRateTone]}
            ariaLabel={t('dashboard.hero_spark_label', { window: windowLabel })}
          />
        </div>
      </section>

      <DashboardUsageSummary today={today} quota={quota} nowMs={nowMs} />

      {/* ---------- KPI ---------- */}
      <section className={styles.statsRow} ref={statsRef} aria-label={t('dashboard.stats_aria')}>
        {statTiles.map((tile) => (
          <article
            key={tile.key}
            className={styles.statTile}
            data-reveal
            style={
              {
                '--tile-accent': tile.tone ? TILE_ACCENTS[tile.tone] : 'var(--border-hover)',
              } as React.CSSProperties
            }
          >
            <span className={styles.statLabel}>{tile.label}</span>
            <strong className={styles.statValue}>{tile.value}</strong>
            {tile.meter !== null && tile.meter !== undefined && (
              <Meter
                value={tile.meter}
                tone={tile.tone}
                ariaLabel={tile.label}
                className={styles.statMeter}
              />
            )}
            <span className={styles.statHint}>{tile.hint}</span>
          </article>
        ))}
      </section>

      {/* ---------- Traffic ---------- */}
      <section className={styles.section} ref={trafficRef}>
        <header className={styles.sectionHead}>
          <span className={styles.eyebrow}>{t('dashboard.traffic_eyebrow')}</span>
          <h2 className={styles.sectionTitle}>{t('dashboard.traffic_title')}</h2>
          <p className={styles.sectionDescription}>
            {t('dashboard.traffic_description', { window: windowLabel })}
          </p>
        </header>
        <div className={styles.panel}>
          <ThroughputChart traffic={traffic} />
        </div>
      </section>

      {/* ---------- Provider fleet ---------- */}
      <section className={styles.section} ref={fleetRef}>
        <header className={styles.sectionHead}>
          <span className={styles.eyebrow}>{t('dashboard.fleet_eyebrow')}</span>
          <h2 className={styles.sectionTitle}>{t('dashboard.fleet_title')}</h2>
          <p className={styles.sectionDescription}>{t('dashboard.fleet_description')}</p>
        </header>
        <div className={styles.panel}>
          {providers.length === 0 ? (
            <p className={styles.emptyNote}>{t('dashboard.fleet_empty')}</p>
          ) : (
            <ul className={styles.fleetList}>
              {providers.map((provider, index) => (
                <li key={provider.id} className={styles.fleetRow}>
                  <span className={styles.fleetRank} aria-hidden="true">
                    {String(index + 1).padStart(2, '0')}
                  </span>
                  <div className={styles.fleetIdentity}>
                    <span className={styles.fleetName}>
                      {providerLabel(provider.id, unknownProviderLabel)}
                    </span>
                    <span className={styles.fleetMeta}>
                      {t('dashboard.fleet_credentials', { value: provider.credentials })}
                    </span>
                  </div>
                  <Sparkline
                    points={provider.buckets.map((bucket) => bucket.success + bucket.failed)}
                    ariaLabel={t('dashboard.fleet_spark_label', {
                      provider: providerLabel(provider.id, unknownProviderLabel),
                    })}
                    className={styles.fleetSpark}
                  />
                  <div className={styles.fleetNumbers}>
                    <span className={styles.fleetTotal}>
                      {trafficKnown ? provider.total.toLocaleString() : DASH}
                    </span>
                    <span className={styles.fleetTotalLabel}>{t('dashboard.fleet_requests')}</span>
                  </div>
                  <div className={styles.fleetRate}>
                    <span className={styles.fleetRateValue}>
                      {provider.successRate === null ? DASH : formatPercent(provider.successRate)}
                    </span>
                    <Meter
                      value={provider.successRate}
                      ariaLabel={t('dashboard.success_rate')}
                      className={styles.fleetMeter}
                    />
                  </div>
                </li>
              ))}
            </ul>
          )}
        </div>
      </section>

      {/* ---------- Credential health + runtime ---------- */}
      <section className={styles.detailGrid} ref={detailRef}>
        <div className={styles.panel} data-reveal>
          <header className={styles.panelHead}>
            <span className={styles.eyebrow}>{t('dashboard.health_eyebrow')}</span>
            <h2 className={styles.panelTitle}>{t('dashboard.health_title')}</h2>
          </header>
          {!credentials ? (
            <p className={styles.emptyNote}>
              {errors.authFiles ? t('dashboard.health_unavailable_note') : t('common.loading')}
            </p>
          ) : credentials.total === 0 ? (
            <p className={styles.emptyNote}>{t('dashboard.health_empty')}</p>
          ) : (
            <>
              <div className={styles.healthBar}>
                {credentials.active > 0 && (
                  <span
                    className={`${styles.healthSegment} ${styles.healthActive}`}
                    style={{ flexGrow: credentials.active }}
                  />
                )}
                {credentials.unavailable > 0 && (
                  <span
                    className={`${styles.healthSegment} ${styles.healthUnavailable}`}
                    style={{ flexGrow: credentials.unavailable }}
                  />
                )}
                {credentials.disabled > 0 && (
                  <span
                    className={`${styles.healthSegment} ${styles.healthDisabled}`}
                    style={{ flexGrow: credentials.disabled }}
                  />
                )}
              </div>
              <ul className={styles.healthLegend}>
                <li>
                  <i className={`${styles.healthKey} ${styles.healthActive}`} aria-hidden="true" />
                  {t('dashboard.health_active')}
                  <b>{credentials.active.toLocaleString()}</b>
                </li>
                <li>
                  <i
                    className={`${styles.healthKey} ${styles.healthUnavailable}`}
                    aria-hidden="true"
                  />
                  {t('dashboard.health_unavailable')}
                  <b>{credentials.unavailable.toLocaleString()}</b>
                </li>
                <li>
                  <i
                    className={`${styles.healthKey} ${styles.healthDisabled}`}
                    aria-hidden="true"
                  />
                  {t('dashboard.health_disabled')}
                  <b>{credentials.disabled.toLocaleString()}</b>
                </li>
              </ul>
              <div className={styles.typeBreakdown}>
                <span className={styles.typeBreakdownLabel}>{t('dashboard.health_by_type')}</span>
                <ul className={styles.typeList}>
                  {credentials.byType.map((entry) => (
                    <li key={entry.type} className={styles.typeChip}>
                      {providerLabel(entry.type, unknownProviderLabel)}
                      <b>{entry.count.toLocaleString()}</b>
                    </li>
                  ))}
                </ul>
              </div>
              <Link to={ROUTES.accounts} className={styles.panelLink}>
                {t('dashboard.health_link')}{' '}
                <span className={styles.linkArrow} aria-hidden="true">
                  →
                </span>
              </Link>
            </>
          )}
        </div>

        <div className={styles.panel} data-reveal>
          <header className={styles.panelHead}>
            <span className={styles.eyebrow}>{t('dashboard.runtime_eyebrow')}</span>
            <h2 className={styles.panelTitle}>{t('dashboard.runtime_title')}</h2>
          </header>
          <dl className={styles.specList}>
            {runtimeRows.map((row) => (
              <div key={row.label} className={styles.specRow}>
                <dt className={styles.specLabel}>{row.label}</dt>
                <dd className={`${styles.specValue} ${row.mono ? styles.specMono : ''}`}>
                  {row.value}
                </dd>
              </div>
            ))}
          </dl>
          {runtimeToggles.length > 0 && (
            <ul className={styles.toggleList}>
              {runtimeToggles.map((toggle) => (
                <li
                  key={toggle.label}
                  className={`${styles.togglePill} ${toggle.on ? styles.toggleOn : styles.toggleOff}`}
                >
                  {toggle.label}
                  <b>{toggle.on ? t('common.yes') : t('common.no')}</b>
                </li>
              ))}
            </ul>
          )}
          <Link to={ROUTES.config} className={styles.panelLink}>
            {t('dashboard.runtime_link')}{' '}
            <span className={styles.linkArrow} aria-hidden="true">
              →
            </span>
          </Link>
        </div>
      </section>

      {/* ---------- CTA ---------- */}
      <section className={styles.section} ref={ctaRef}>
        <header className={styles.sectionHead} data-reveal>
          <span className={styles.eyebrow}>{t('dashboard.cta_eyebrow')}</span>
          <h2 className={styles.sectionTitle}>{t('dashboard.cta_title')}</h2>
        </header>
        <div className={styles.ctaGrid}>
          {ctaCards.map((card) => (
            <Link key={card.to} to={card.to} className={styles.ctaCard} data-reveal>
              <span className={styles.ctaIcon}>{card.icon}</span>
              <span className={styles.ctaTitle}>{card.title}</span>
              <span className={styles.ctaDescription}>{card.description}</span>
              <span className={styles.ctaArrow} aria-hidden="true">
                →
              </span>
            </Link>
          ))}
        </div>
      </section>
    </div>
  );
}
