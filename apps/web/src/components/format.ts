import { copy } from '../copy';

const TINY_COST = 0.000001;

export function formatCost(value: number | null | undefined): string {
  if (value === null || value === undefined || !Number.isFinite(value)) {
    return copy.usageUnknown;
  }
  if (value === 0) return '¥0.00';
  if (Math.abs(value) < TINY_COST) return '< ¥0.000001';
  return value.toLocaleString('zh-CN', {
    style: 'currency',
    currency: 'CNY',
    minimumFractionDigits: 2,
    maximumFractionDigits: 6,
  });
}

export function formatUnitPrice(value: number | null | undefined): string {
  if (value === null || value === undefined || !Number.isFinite(value)) {
    return copy.usageUnknown;
  }
  return `¥${value.toLocaleString('zh-CN', {
    minimumFractionDigits: 2,
    maximumFractionDigits: 4,
  })}`;
}
