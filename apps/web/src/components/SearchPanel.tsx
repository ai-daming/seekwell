import type { FormEvent } from 'react';
import { copy } from '../copy';
import { Icon } from './Icon';
import './SearchPanel.css';

type SearchPanelProps = {
  query: string;
  searching: boolean;
  result: string;
  onQueryChange: (value: string) => void;
  onSubmit: (event: FormEvent<HTMLFormElement>) => void;
};

export function SearchPanel({
  query,
  searching,
  result,
  onQueryChange,
  onSubmit,
}: SearchPanelProps) {
  return (
    <>
      <div className="notice">
        <Icon name="info" />
        <span>{copy.searchBillingNotice}</span>
      </div>
      <section className="search-panel">
        <form onSubmit={onSubmit}>
          <label className="field" htmlFor="search-query">
            搜索内容
          </label>
          <div className="search-row">
            <input
              id="search-query"
              required
              value={query}
              onChange={(event) => onQueryChange(event.target.value)}
              placeholder="输入搜索关键词"
            />
            <button type="submit" className="button button--primary" disabled={searching}>
              <Icon name="search" />
              {searching ? '搜索中' : '搜索'}
            </button>
          </div>
          <small className="form-hint">{copy.searchCount}</small>
        </form>
        {result && <pre className="search-result">{result}</pre>}
      </section>
    </>
  );
}
