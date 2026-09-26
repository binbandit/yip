<script lang="ts" module>
  // 24px stroke icons drawn for yip (1.75px strokes, round caps).
  const PATHS: Record<string, string> = {
    search: '<circle cx="11" cy="11" r="6.5"/><path d="m16 16 4.5 4.5"/>',
    plus: '<path d="M12 5v14M5 12h14"/>',
    x: '<path d="m6 6 12 12M18 6 6 18"/>',
    lock: '<rect x="5" y="10.5" width="14" height="10" rx="2.5"/><path d="M8.5 10.5V8a3.5 3.5 0 0 1 7 0v2.5"/>',
    chevronDown: '<path d="m6.5 9.5 5.5 5.5 5.5-5.5"/>',
    chevronRight: '<path d="m9.5 6.5 5.5 5.5-5.5 5.5"/>',
    chevronLeft: '<path d="m14.5 6.5-5.5 5.5 5.5 5.5"/>',
    check: '<path d="m5.5 12.5 4 4 9-9.5"/>',
    sun: '<circle cx="12" cy="12" r="4"/><path d="M12 2.5v2M12 19.5v2M4.6 4.6 6 6M18 18l1.4 1.4M2.5 12h2M19.5 12h2M4.6 19.4 6 18M18 6l1.4-1.4"/>',
    moon: '<path d="M19.5 14.5A8 8 0 0 1 9.5 4.5a7.5 7.5 0 1 0 10 10Z"/>',
    settings: '<circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.6 1.6 0 0 0 .3 1.8l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.6 1.6 0 0 0-1.8-.3 1.6 1.6 0 0 0-1 1.5V21a2 2 0 1 1-4 0v-.1a1.6 1.6 0 0 0-1-1.5 1.6 1.6 0 0 0-1.8.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1.6 1.6 0 0 0 .3-1.8 1.6 1.6 0 0 0-1.5-1H3a2 2 0 1 1 0-4h.1a1.6 1.6 0 0 0 1.5-1 1.6 1.6 0 0 0-.3-1.8l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1a1.6 1.6 0 0 0 1.8.3H9a1.6 1.6 0 0 0 1-1.5V3a2 2 0 1 1 4 0v.1a1.6 1.6 0 0 0 1 1.5 1.6 1.6 0 0 0 1.8-.3l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.6 1.6 0 0 0-.3 1.8V9a1.6 1.6 0 0 0 1.5 1H21a2 2 0 1 1 0 4h-.1a1.6 1.6 0 0 0-1.5 1Z"/>',
    logout: '<path d="M14 4h3.5A2.5 2.5 0 0 1 20 6.5v11a2.5 2.5 0 0 1-2.5 2.5H14"/><path d="M10 16.5 5.5 12 10 7.5M5.5 12H15"/>',
    menu: '<path d="M4 7h16M4 12h16M4 17h16"/>',
    reply: '<path d="M20 15.5a2.5 2.5 0 0 1-2.5 2.5H9l-4.5 3.5V6.5A2.5 2.5 0 0 1 7 4h10.5A2.5 2.5 0 0 1 20 6.5Z"/>',
    smile: '<circle cx="12" cy="12" r="8.5"/><path d="M8.5 14a4 4 0 0 0 7 0M9 9.5h.01M15 9.5h.01"/>',
    link: '<path d="M10 14a4 4 0 0 0 5.7 0l3-3a4 4 0 0 0-5.7-5.7l-1 1"/><path d="M14 10a4 4 0 0 0-5.7 0l-3 3a4 4 0 0 0 5.7 5.7l1-1"/>',
    more: '<circle cx="6" cy="12" r="1.2"/><circle cx="12" cy="12" r="1.2"/><circle cx="18" cy="12" r="1.2"/>',
    send: '<path d="M12 19V5.5M6 11l6-6 6 6"/>',
    users: '<circle cx="9" cy="8.5" r="3.5"/><path d="M2.5 20a6.5 6.5 0 0 1 13 0"/><path d="M16 5.2a3.5 3.5 0 0 1 0 6.6M18 14.3a6.5 6.5 0 0 1 3.5 5.7"/>',
    folder: '<path d="M3.5 7.5A2.5 2.5 0 0 1 6 5h3.6l2 2.2H18a2.5 2.5 0 0 1 2.5 2.5v7.8A2.5 2.5 0 0 1 18 20H6a2.5 2.5 0 0 1-2.5-2.5Z"/>',
    machine: '<rect x="3.5" y="4.5" width="17" height="11" rx="2"/><path d="M8 20h8M12 15.5V20"/>',
    overview: '<path d="M4 5.5h7v6H4zM13 5.5h7v3.5h-7zM13 11h7v7.5h-7zM4 13.5h7v5H4z"/>',
    file: '<path d="M14 3.5H7.5A2 2 0 0 0 5.5 5.5v13a2 2 0 0 0 2 2h9a2 2 0 0 0 2-2V8Z"/><path d="M14 3.5V8h4.5"/>',
    pr: '<circle cx="6.5" cy="5.5" r="2"/><circle cx="6.5" cy="18.5" r="2"/><circle cx="17.5" cy="18.5" r="2"/><path d="M6.5 7.5v9M17.5 16.5V9a3 3 0 0 0-3-3H11m2-2.5L10.5 6 13 8.5"/>',
    commit: '<circle cx="12" cy="12" r="3.5"/><path d="M3 12h5.5M15.5 12H21"/>',
    terminal: '<rect x="3.5" y="4.5" width="17" height="15" rx="2.5"/><path d="m7.5 9.5 3 2.5-3 2.5M12.5 15h4"/>',
    clock: '<circle cx="12" cy="12" r="8.5"/><path d="M12 7.5V12l3 2"/>',
    refresh: '<path d="M19.5 12a7.5 7.5 0 0 1-13 5.1M4.5 12a7.5 7.5 0 0 1 13-5.1"/><path d="M17.5 3.5v3.6h-3.6M6.5 20.5v-3.6h3.6"/>',
    stop: '<rect x="6.5" y="6.5" width="11" height="11" rx="2"/>',
    external: '<path d="M14 4.5h5.5V10M19.5 4.5 11 13"/><path d="M17.5 14v3.5a2 2 0 0 1-2 2h-9a2 2 0 0 1-2-2v-9a2 2 0 0 1 2-2H10"/>',
    copy: '<rect x="8.5" y="8.5" width="11" height="11" rx="2"/><path d="M15.5 8.5V6.5a2 2 0 0 0-2-2h-7a2 2 0 0 0-2 2v7a2 2 0 0 0 2 2h2"/>',
    pencil: '<path d="M15.5 5 19 8.5 9 18.5l-4.5 1 1-4.5Z"/>',
    bellOff: '<path d="M9.5 19.5a2.6 2.6 0 0 0 5 0"/><path d="M17.5 13.2V10a5.5 5.5 0 0 0-8.4-4.7M6.6 8.4A5.6 5.6 0 0 0 6.5 10v4.5L4.5 17h12"/><path d="m4 4 16 16"/>',
    trash: '<path d="M4.5 7h15M9.5 7V5h5v2M6.5 7l1 12.5h9l1-12.5"/>',
    back: '<path d="M19 12H5.5M11 6l-6 6 6 6"/>',
    at: '<circle cx="12" cy="12" r="3.5"/><path d="M15.5 12v1.5a2.5 2.5 0 0 0 5 0V12a8.5 8.5 0 1 0-3.3 6.7"/>',
    eye: '<path d="M2.5 12S6 5.5 12 5.5 21.5 12 21.5 12 18 18.5 12 18.5 2.5 12 2.5 12Z"/><circle cx="12" cy="12" r="3"/>',
    shield: '<path d="M12 3.5 19 6v5.5c0 4.5-3 7.6-7 9-4-1.4-7-4.5-7-9V6Z"/>',
    book: '<path d="M5 5.5A2 2 0 0 1 7 3.5h12v15H7a2 2 0 0 0-2 2Z"/><path d="M5 20.5a2 2 0 0 1 2-2h12v2H7"/>',
    hash: '<path d="M9.5 3.5 7.5 20.5M16.5 3.5l-2 17M4.5 9h16M3.5 15h16"/>',
    download: '<path d="M12 4v11M7 10.5l5 5 5-5M5 19.5h14"/>',
    drag: '<path d="M9 5.5h.01M15 5.5h.01M9 12h.01M15 12h.01M9 18.5h.01M15 18.5h.01"/>',
    expand: '<path d="M14.5 4.5h5v5M9.5 19.5h-5v-5M19.5 4.5 13.5 10.5M4.5 19.5l6-6"/>',
    collapse: '<path d="M19.5 9.5h-5v-5M4.5 14.5h5v5M14.5 9.5l6-6M9.5 14.5l-6 6"/>',
    info: '<circle cx="12" cy="12" r="8.5"/><path d="M12 11v5.5M12 7.5h.01"/>',
    alert: '<path d="M12 4 21 19.5H3Z"/><path d="M12 10v4.5M12 17h.01"/>',
    wifiOff: '<path d="M3 3l18 18M8.5 16.5a5 5 0 0 1 7 0M5 13a10 10 0 0 1 4-2.4M19 13a10 10 0 0 0-2.4-1.7M2 9.5a15 15 0 0 1 4.6-2.8M22 9.5A15 15 0 0 0 11 5.6M12 20h.01"/>',
    key: '<circle cx="8" cy="15.5" r="4"/><path d="m11 12.5 8.5-8.5M16 7l2.5 2.5M14 9l2 2"/>',
    tag: '<path d="M3.5 12.3V4.5a1 1 0 0 1 1-1h7.8l8.2 8.2a1.5 1.5 0 0 1 0 2.1l-6.7 6.7a1.5 1.5 0 0 1-2.1 0Z"/><circle cx="8" cy="8" r="1.3"/>',
    history: '<path d="M4.5 12a7.5 7.5 0 1 0 2.2-5.3L4.5 9"/><path d="M4.5 4.5V9H9M12 8v4.5l3 1.8"/>',
    grip: '<path d="M10 4v16M14 4v16"/>',
  };
  export const ICONS = Object.keys(PATHS);
</script>

<script lang="ts">
  interface Props {
    name: string;
    size?: number;
    label?: string;
    class?: string;
  }
  let { name, size = 18, label, class: cls = '' }: Props = $props();
  const body = $derived(PATHS[name] ?? PATHS.info);
</script>

<svg
  class="icon {cls}"
  width={size}
  height={size}
  viewBox="0 0 24 24"
  fill="none"
  stroke="currentColor"
  stroke-width="1.75"
  stroke-linecap="round"
  stroke-linejoin="round"
  role={label ? 'img' : undefined}
  aria-label={label}
  aria-hidden={label ? undefined : 'true'}
  focusable="false"
>
  <!-- eslint-disable-next-line svelte/no-at-html-tags -- static, trusted path data -->
  {@html body}
</svg>

<style>
  .icon {
    flex: none;
    display: block;
  }
</style>
