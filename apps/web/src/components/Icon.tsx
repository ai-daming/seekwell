import { icons } from '../copy';

export type IconName = keyof typeof icons;

export function Icon({ name }: { name: IconName }) {
  if (name === 'brand') {
    return <img className="brand-icon" src="/logo.svg" alt="" aria-hidden="true" />;
  }

  return (
    <span className="emoji" aria-hidden="true">
      {icons[name]}
    </span>
  );
}
