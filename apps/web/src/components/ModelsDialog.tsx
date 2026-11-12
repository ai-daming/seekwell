import { copy } from '../copy';
import { Icon } from './Icon';
import { useDialogFocus } from './useDialogFocus';
import './ModelsDialog.css';

type ModelsDialogProps = {
  discovered: { name: string; models: string[] } | null;
  onClose: () => void;
};

export function ModelsDialog({ discovered, onClose }: ModelsDialogProps) {
  const panelRef = useDialogFocus(discovered !== null, onClose);

  if (!discovered) return null;

  return (
    <div className="overlay" role="presentation">
      <div
        ref={panelRef}
        className="dialog"
        role="dialog"
        aria-modal="true"
        aria-labelledby="models-dialog-title"
      >
        <div className="dialog__head">
          <div>
            <div className="eyebrow">AVAILABLE MODELS</div>
            <h2 id="models-dialog-title">{discovered.name} · 模型目录</h2>
            <p>{copy.catalogUnverified}</p>
          </div>
          <button type="button" className="button button--ghost" onClick={onClose}>
            <Icon name="close" /> 关闭
          </button>
        </div>

        <div className="model-results">
          {discovered.models.length > 0 ? (
            discovered.models.map((model) => (
              <div className="model-results__item mono" key={model}>
                {model}
              </div>
            ))
          ) : (
            <p className="model-results__empty">渠道没有返回模型。</p>
          )}
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
