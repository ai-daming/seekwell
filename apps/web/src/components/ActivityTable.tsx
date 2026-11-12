import { copy } from '../copy';
import type { Entry } from '../types';
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
            <th>状态码</th>
            <th>耗时</th>
          </tr>
        </thead>
        <tbody>
          {entries.map((entry, index) => (
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
                <span
                  className={`activity-status ${entry.status === 0 ? 'activity-status--error' : ''}`}
                >
                  {entry.status === 0 ? copy.statusZero : entry.status}
                </span>
              </td>
              <td>
                <span className="activity-duration">{entry.duration_ms ?? 0} ms</span>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
