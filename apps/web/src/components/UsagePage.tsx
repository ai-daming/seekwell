import { copy } from '../copy';
import type { PricingRate, UsageDaily, UsageKeyStats, UsagePricing, UsageStats } from '../types';
import { Icon } from './Icon';
import { formatCost, formatUnitPrice } from './format';
import './Channels.css';
import './UsagePage.css';

type UsagePageProps = {
  stats: UsageStats | null;
  loading: boolean;
  error: string;
  onRetry: () => void;
  onGoToKeys: () => void;
};

function formatCount(value: number): string {
  return value.toLocaleString('zh-CN');
}

function formatNumber(value: number | null): string {
  return value === null ? copy.usageUnknown : formatCount(value);
}

function formatRate(successCount: number, requestCount: number): string {
  if (requestCount === 0) return '—';
  return `${((successCount / requestCount) * 100).toLocaleString('zh-CN', { maximumFractionDigits: 1 })}%`;
}

function formatTime(value: string | null): string {
  if (!value) return copy.usageNeverUsed;
  try {
    const date = new Date(value);
    if (Number.isNaN(date.getTime())) return value;
    return date.toLocaleString('zh-CN');
  } catch {
    return value;
  }
}

function formatDay(value: string): string {
  try {
    const parts = value.split('-').map(Number);
    if (parts.length !== 3 || parts.some((part) => !Number.isFinite(part))) return value;
    const date = new Date(parts[0], parts[1] - 1, parts[2]);
    if (Number.isNaN(date.getTime())) return value;
    return date.toLocaleDateString('zh-CN', { month: '2-digit', day: '2-digit' });
  } catch {
    return value;
  }
}

function Metric({ label, value, note }: { label: string; value: string; note: string }) {
  return (
    <div className="usage-metric">
      <span className="usage-metric__label">{label}</span>
      <strong className="usage-metric__value">{value}</strong>
      <span className="usage-metric__note">{note}</span>
    </div>
  );
}

function DailyBars({ daily }: { daily: UsageDaily[] }) {
  const maxRequests = Math.max(...daily.map((day) => day.request_count), 0);
  if (maxRequests === 0) {
    return (
      <div className="empty">
        <div className="empty__icon">
          <Icon name="empty" />
        </div>
        <strong>{copy.usageDailyEmpty}</strong>
        <p>在近 7 天窗口内发起带访问 Key 的 /v1 POST 请求后，这里会显示每天的使用情况。</p>
      </div>
    );
  }
  return (
    <div className="usage-days">
      {daily.map((day) => (
        <div className="usage-day" key={day.date}>
          <span className="usage-day__date">{formatDay(day.date)}</span>
          <div className="usage-day__bar" aria-hidden="true">
            <span
              className="usage-day__fill"
              style={{ width: `${maxRequests === 0 ? 0 : (day.request_count / maxRequests) * 100}%` }}
            />
          </div>
          <span className="usage-day__count">
            {formatCount(day.request_count)} {copy.usageRequestColumn}
          </span>
          <span className="usage-day__tokens">
            {copy.usageTotalTokenColumn} {formatNumber(day.total_tokens)} · {copy.usageCostColumn} {formatCost(day.cost_cny)}
          </span>
        </div>
      ))}
    </div>
  );
}

function KeyRow({ item }: { item: UsageKeyStats }) {
  return (
    <tr>
      <td>
        <div className="usage-key">
          <b className="usage-key__name">{item.name}</b>
          <small className="cell-sub mono">{item.prefix}…</small>
        </div>
      </td>
      <td>
        <span className={`status ${item.enabled ? '' : 'status--off'}`}>
          <span className="status__dot" />
          {item.enabled ? copy.usageEnabled : copy.usageDisabled}
        </span>
      </td>
      <td className="usage-numeric">{formatCount(item.request_count)}</td>
      <td className="usage-numeric">
        {formatCount(item.success_count)} / {formatCount(item.failure_count)}
      </td>
      <td className="usage-numeric">{formatRate(item.success_count, item.request_count)}</td>
      <td className="usage-numeric">
        {formatNumber(item.input_tokens)} / {formatNumber(item.output_tokens)}
      </td>
      <td className="usage-numeric">{formatNumber(item.total_tokens)}</td>
      <td className="usage-numeric">
        {formatCost(item.cost_cny)}
        <small className="cell-sub">
          {copy.usageCostKnown} {formatCount(item.cost_known_count)} / {formatCount(item.request_count)}
        </small>
      </td>
      <td className="usage-numeric">
        {formatCount(item.usage_known_count)} / {formatCount(item.request_count)}
        <small className="cell-sub">{copy.usageUnknown} {formatCount(item.usage_unknown_count)}</small>
      </td>
      <td className="usage-time">{formatTime(item.last_request_at)}</td>
    </tr>
  );
}

function PricingNotes({ pricing }: { pricing: UsagePricing }) {
  const year = Number(
    new Date().toLocaleString('en-US', { timeZone: pricing.peak_timezone, year: 'numeric' }),
  );
  const holidayCovered = pricing.holiday_years.includes(year);
  const warning = pricing.stale || !pricing.last_sync_ok;
  return (
    <div className={`usage-pricing${warning ? ' usage-pricing--warning' : ''}`}>
      <p>{copy.usagePricingEstimate}</p>
      <p>{copy.usagePricingExclusions}</p>
      <p>{copy.usageCostUnknown}</p>
      <p>
        {copy.usagePricingSnapshot}：
        {pricing.source === 'bundled'
          ? copy.usagePricingBundled
          : formatTime(pricing.last_sync_at)}
        {pricing.stale ? ` · ${copy.usagePricingStale}` : ''}
        {` · ${copy.usagePricingPeakRule}`}
      </p>
      {!pricing.last_sync_ok && <p>{copy.usagePricingSyncFailed}</p>}
      {pricing.warning && <p>{pricing.warning}</p>}
      {!holidayCovered && <p>{copy.usageHolidayUncovered}</p>}
    </div>
  );
}

const pricingColumns = [
  { key: 'hit_peak', label: copy.usagePricingHitPeak },
  { key: 'miss_peak', label: copy.usagePricingMissPeak },
  { key: 'out_peak', label: copy.usagePricingOutPeak },
  { key: 'hit_off', label: copy.usagePricingHitOff },
  { key: 'miss_off', label: copy.usagePricingMissOff },
  { key: 'out_off', label: copy.usagePricingOutOff },
] as const;

type PricingValueKey = (typeof pricingColumns)[number]['key'];

function pricingValue(rate: PricingRate, key: PricingValueKey): number {
  return rate[key];
}

function CurrentPricing({ pricing, models }: { pricing: UsagePricing; models: PricingRate[] }) {
  const source =
    pricing.source === 'bundled'
      ? copy.usagePricingBundled
      : `${copy.usagePricingRemote} ${formatTime(pricing.last_sync_at)}`;
  const warning = pricing.stale || !pricing.last_sync_ok;
  return (
    <section className="usage-section">
      <div className="section-head">
        <div>
          <div className="eyebrow">OFFICIAL PRICE</div>
          <h2>{copy.usagePricingCurrent}</h2>
        </div>
      </div>
      <p className="usage-table-note">
        {copy.usagePricingUnit} · {source}
        {warning ? ` · ${copy.usagePricingMayBeStale}` : ''}
      </p>
      <p className="usage-table-note">{copy.usagePricingAliasNotice}</p>
      <p className="usage-table-note">{copy.usagePricingNonOfficialNotice}</p>
      <div className="table-scroll">
        <table>
          <caption className="sr-only">{copy.usagePricingCurrentCaption}</caption>
          <thead>
            <tr>
              <th>{copy.usagePricingModel}</th>
              {pricingColumns.map((column) => (
                <th key={column.key}>{column.label}</th>
              ))}
            </tr>
          </thead>
          <tbody>
            {models.map((rate) => (
              <tr key={rate.model}>
                <td>
                  <span className="mono">{rate.model}</span>
                </td>
                {pricingColumns.map((column) => (
                  <td className="usage-numeric" key={column.key}>
                    {formatUnitPrice(pricingValue(rate, column.key))}
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
}

function PricingHistoryTable({ pricing, history }: { pricing: UsagePricing; history: UsagePricing['history'] }) {
  return (
    <section className="usage-section">
      <div className="section-head">
        <div>
          <div className="eyebrow">OBSERVED SNAPSHOTS</div>
          <h2>{copy.usagePricingHistory}</h2>
        </div>
      </div>
      <p className="usage-table-note">{copy.usagePricingHistoryNotExact}</p>
      <p className="usage-table-note">{copy.usagePricingHistoryNoBackfill}</p>
      {history.length === 0 ? (
        <div className="empty">
          <div className="empty__icon">
            <Icon name="empty" />
          </div>
          <strong>{copy.usagePricingHistoryEmpty}</strong>
        </div>
      ) : (
        <div className="table-scroll">
          <table>
            <caption className="sr-only">{copy.usagePricingHistoryCaption}</caption>
            <thead>
              <tr>
                <th>{copy.usagePricingHistoryObservedAt}</th>
                <th>{copy.usagePricingHistorySource}</th>
                <th>{copy.usagePricingModel}</th>
                {pricingColumns.map((column) => (
                  <th key={column.key}>{column.label}</th>
                ))}
                <th>{copy.usagePricingHistoryChange}</th>
              </tr>
            </thead>
            <tbody>
              {history.map((snapshot, index) => {
                const older = history[index + 1];
                return snapshot.models.map((rate) => {
                  const olderRate = older?.models.find((item) => item.model === rate.model);
                  const baseline = older === undefined;
                  const added = older !== undefined && olderRate === undefined;
                  return (
                    <tr key={`${index}-${snapshot.version}-${snapshot.observed_at}-${rate.model}`}>
                      <td className="usage-time">
                        <span title={snapshot.observed_at}>{formatTime(snapshot.observed_at)}</span>
                      </td>
                      <td>
                        {snapshot.source === 'bundled' ? copy.usagePricingBundled : copy.usagePricingRemote}
                      </td>
                      <td>
                        <span className="mono">{rate.model}</span>
                      </td>
                      {pricingColumns.map((column) => {
                        const value = pricingValue(rate, column.key);
                        const oldValue = olderRate ? pricingValue(olderRate, column.key) : undefined;
                        const changed = oldValue !== undefined && oldValue !== value;
                        const label = changed
                          ? `${formatUnitPrice(oldValue)} → ${formatUnitPrice(value)}`
                          : undefined;
                        return (
                          <td
                            className={`usage-numeric${changed ? ' usage-price--changed' : ''}`}
                            key={column.key}
                            title={label}
                            aria-label={label}
                          >
                            {formatUnitPrice(value)}
                          </td>
                        );
                      })}
                      <td className="usage-history-tag">
                        {baseline
                          ? copy.usagePricingHistoryBaseline
                          : added
                            ? copy.usagePricingHistoryNewModel
                            : '—'}
                      </td>
                    </tr>
                  );
                });
              })}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}

export function UsagePage({ stats, loading, error, onRetry, onGoToKeys }: UsagePageProps) {
  return (
    <>
      <div className="page-head">
        <div>
          <div className="eyebrow">USAGE STATISTICS</div>
          <h1>使用统计</h1>
          <p>查看全部访问 Key 的请求、Token 与近期使用趋势。</p>
        </div>
        <button type="button" className="button button--ghost" onClick={onRetry} disabled={loading}>
          <Icon name="refresh" /> {loading ? '刷新中' : '刷新'}
        </button>
      </div>

      <div className="notice">
        <Icon name="info" />
        <span>{copy.usageScopeNotice}</span>
      </div>

      {error && (
        <div className="error usage-error" role="alert">
          <span>{copy.usageStatsFailed}：{error}</span>
          <button type="button" className="button button--ghost" onClick={onRetry} disabled={loading}>
            {copy.usageStatsRetry}
          </button>
        </div>
      )}

      {loading && !stats ? (
        <div className="empty">{copy.usageStatsLoading}</div>
      ) : stats ? (
        <>
          <div className="usage-metrics">
            <Metric
              label={copy.usageTotalRequests}
              value={formatCount(stats.overall.request_count)}
              note={`${copy.usageSuccessFailure} ${formatCount(stats.overall.success_count)} / ${formatCount(stats.overall.failure_count)}`}
            />
            <Metric
              label={copy.usageSuccessRate}
              value={formatRate(stats.overall.success_count, stats.overall.request_count)}
              note={`${copy.usageTotalRequests} ${formatCount(stats.overall.request_count)}`}
            />
            <Metric
              label={copy.usageTotalTokens}
              value={formatNumber(stats.overall.total_tokens)}
              note={`${copy.usageInputOutput} ${formatNumber(stats.overall.input_tokens)} / ${formatNumber(stats.overall.output_tokens)}`}
            />
            <Metric
              label={copy.usageUsageKnown}
              value={
                stats.overall.request_count === 0
                  ? '—'
                  : `${formatCount(stats.overall.usage_known_count)} / ${formatCount(stats.overall.request_count)}`
              }
              note={`${copy.usageUnknown} ${formatCount(stats.overall.usage_unknown_count)}`}
            />
            <Metric
              label={copy.usageCost}
              value={formatCost(stats.overall.cost_cny)}
              note={`${copy.usageCostKnown} ${formatCount(stats.overall.cost_known_count)} / ${formatCount(stats.overall.request_count)} · ${copy.usageUnknown} ${formatCount(stats.overall.cost_unknown_count)}`}
            />
            <Metric
              label={copy.usageActiveKeys}
              value={`${formatCount(stats.overall.enabled_key_count)} / ${formatCount(stats.overall.key_count)}`}
              note={`${formatCount(stats.overall.enabled_key_count)} ${copy.usageEnabled}`}
            />
          </div>

          <PricingNotes pricing={stats.pricing} />

          <CurrentPricing pricing={stats.pricing} models={stats.pricing.models} />
          <PricingHistoryTable pricing={stats.pricing} history={stats.pricing.history} />

          <section className="usage-section">
            <div className="section-head">
              <div>
                <div className="eyebrow">LAST 7 DAYS</div>
                <h2>{copy.usageRecentSevenDays}</h2>
              </div>
            </div>
            <DailyBars daily={stats.daily} />
          </section>

          <section className="usage-section">
            <div className="section-head">
              <div>
                <div className="eyebrow">PER KEY</div>
                <h2>
                  {copy.usagePerKey} <span>{stats.keys.length}</span>
                </h2>
              </div>
            </div>
            {stats.keys.length === 0 ? (
              <div className="empty">
                <div className="empty__icon">
                  <Icon name="empty" />
                </div>
                <strong>{copy.usageStatsEmptyKeys}</strong>
                <p>{copy.usageStatsEmptyKeysHint}</p>
                <button type="button" className="button button--primary" onClick={onGoToKeys}>
                  {copy.usageGoKeys}
                </button>
              </div>
            ) : (
              <div className="table-scroll">
                <table>
                  <caption className="sr-only">{copy.usageTableCaption}</caption>
                  <thead>
                    <tr>
                      <th>{copy.usageKeyColumn}</th>
                      <th>{copy.usageStatusColumn}</th>
                      <th>{copy.usageRequestColumn}</th>
                      <th>{copy.usageSuccessFailure}</th>
                      <th>{copy.usageSuccessRate}</th>
                      <th>{copy.usageInputOutput}</th>
                      <th>{copy.usageTotalTokenColumn}</th>
                      <th>{copy.usageCostColumn}</th>
                      <th>{copy.usageUsageKnown}</th>
                      <th>{copy.usageLastRequest}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {stats.keys.map((item) => (
                      <KeyRow item={item} key={item.id} />
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </section>
        </>
      ) : null}
    </>
  );
}
