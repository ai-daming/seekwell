import { icons } from '../copy';

export type IconName = keyof typeof icons;

export function Icon({ name }: { name: IconName }) {
  return (
    <span className="emoji" aria-hidden="true">
      {icons[name]}
    </span>
  );
}
