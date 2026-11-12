import { copy } from '../copy';
import type { SpeedTestResult } from '../types';
import { Icon } from './Icon';
import { useDialogFocus } from './useDialogFocus';
import './SpeedDialog.css';

type SpeedDialogProps = {
  measured: { name: string; result: SpeedTestResult } | null;
  onClose: () => void;
};

export function SpeedDialog({ measured, onClose }: SpeedDialogProps) {
  const panelRef = useDialogFocus(measured !== null, onClose);

  if (!measured) return null;

  const { result } = measured;

  return (
    <div className="overlay" role="presentation">
      <div
        ref={panelRef}
        className="dialog"
        role="dialog"
        aria-modal="true"
        aria-labelledby="speed-dialog-title"
      >
        <div className="dialog__head">
          <div>
            <div className="eyebrow">SPEED TEST</div>
            <h2 id="speed-dialog-title">{measured.name} · 测速结果</h2>
            <p>{copy.speedTestNotice}</p>
          </div>
          <button type="button" className="button button--ghost" onClick={onClose}>
            <Icon name="close" /> 关闭
          </button>
        </div>

        <div className="speed-results">
          <div className="speed-results__item">
            <span>总耗时</span>
            <strong>{result.duration_ms} ms</strong>
          </div>
          <div className="speed-results__item">
            <span>上游状态码</span>
            <strong>{result.status}</strong>
          </div>
          <div className="speed-results__item">
            <span>所测模型</span>
            <strong className="mono">{result.model}</strong>
          </div>
        </div>

        <div className="dialog__actions">
          <button type="button" className="button button--primary" onClick={onClose}>
            完成
          </button>
        </div>
      </div>
    </div>
  );
}
