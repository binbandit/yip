// Drives Astryx controls the way a user would in jsdom.
import { flushSync } from 'svelte';

/** Opens an Astryx Selector and picks the option whose text starts with `label`. */
export function choose(trigger: HTMLElement, label: string): void {
  trigger.click();
  flushSync();
  const list = document.getElementById(trigger.getAttribute('aria-controls') ?? '');
  const option = [...(list ?? document).querySelectorAll<HTMLElement>('[role=option]')].find((o) => o.textContent?.trim().startsWith(label));
  if (!option) throw new Error(`No option "${label}" for ${trigger.outerHTML.slice(0, 120)}`);
  option.click();
  flushSync();
}
