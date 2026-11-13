import { copy } from '../copy';
import { kindLabel, type Entry } from '../types';
import { Icon } from './Icon';
import './Activity.css';
import './Channels.css';

function formatTime(value: string): string {
  try {
    const date = new Date(value);
    if (Number.isNaN(date.getTime())) return value;
    return date.toLocaleString('zh-CN');
  } catch {
    return value;
  }
}

function tokenText(entry: Entry, value: number | null | undefined): string {
  if (entry.status === 0 || value === null || value === undefined) return copy.usageUnknown;
  return String(value);
}

function cacheHitRate(entry: Entry): number | null {
  if (entry.status === 0) return null;
  const hit = entry.cache_hit_tokens;
  const miss = entry.cache_miss_tokens;
  const input = entry.input_tokens;
  if (hit === null || hit === undefined) return null;
  let denominator = miss === null || miss === undefined ? 0 : hit + miss;
  if (denominator <= 0 && input !== null && input !== undefined && input > 0) {
    denominator = input;
  }
  if (denominator <= 0) return null;
  const rate = (hit / denominator) * 100;
  return Number.isFinite(rate) ? rate : null;
}

function rateText(entry: Entry): string {
  const rate = cacheHitRate(entry);
  return rate === null ? copy.usageUnknown : `${rate.toFixed(1)}%`;
}

export function ActivityTable({ entries }: { entries: Entry[] }) {
  if (entries.length === 0) {
    return (
      <div className="empty">
        <div className="empty__icon">
          <Icon name="empty" />
        </div>
        <strong>{copy.emptyActivity}</strong>
        <p>通过统一 API 发起模型请求后，记录会出现在这里。</p>
      </div>
    );
  }

  return (
    <div className="table-scroll">
      <table>
        <caption className="sr-only">调用记录</caption>
        <thead>
          <tr>
            <th>时间</th>
            <th>统一模型</th>
            <th>渠道</th>
            <th>渠道类型</th>
            <th>状态码</th>
            <th>输入 tokens</th>
            <th>输出 tokens</th>
            <th>缓存命中率</th>
            <th>耗时</th>
          </tr>
        </thead>
        <tbody>
          {entries.map((entry, index) => {
            const { status } = entry;
            const failed = status === 0 || status >= 400;
            return (
              <tr key={`${entry.created_at}-${entry.model}-${index}`}>
                <td>
                  <span className="activity-time">{formatTime(entry.created_at)}</span>
                </td>
                <td>
                  <span className="activity-model mono">{entry.model}</span>
                </td>
                <td>
                  <span className="activity-channel">{entry.channel}</span>
                </td>
                <td>
                  <span className="activity-channel">{kindLabel(entry.kind)}</span>
                </td>
                <td>
                  <span className={`activity-status ${failed ? 'activity-status--error' : ''}`}>
                    {status === 0 ? copy.statusZero : status}
                  </span>
                </td>
                <td>
                  <span className="activity-token">{tokenText(entry, entry.input_tokens)}</span>
                </td>
                <td>
                  <span className="activity-token">{tokenText(entry, entry.output_tokens)}</span>
                </td>
                <td>
                  <span className="activity-token">{rateText(entry)}</span>
                </td>
                <td>
                  <span className="activity-duration">{entry.duration_ms ?? 0} ms</span>
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
