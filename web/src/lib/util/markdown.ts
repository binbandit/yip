// Markdown-lite: the small subset engineers and owners actually use in chat.
//
// Supported: paragraphs, line breaks, `inline code`, fenced code blocks,
// bulleted and numbered lists, > quotes, **bold**, *emphasis*, [links](https://…),
// bare http(s) links, #-headings (rendered as strong lines), and highlighted
// structured mentions. Everything else is text. Raw HTML is always escaped:
// the output never contains a tag that did not come from this renderer, and
// link targets are limited to http, https and mailto.

export interface MentionLabel {
  kind: string;
  id: string;
  label: string;
}

export interface RenderOptions {
  /** Structured mentions by lowercase handle; only these are highlighted. */
  mentions?: Map<string, MentionLabel>;
}

export function escapeHtml(s: string): string {
  return s.replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c]!);
}

export function safeHref(url: string): string | null {
  const u = url.trim();
  if (/^(https?:\/\/|mailto:)/i.test(u)) return u;
  return null;
}

type Block =
  | { t: 'code'; lang: string; text: string }
  | { t: 'p'; lines: string[] }
  | { t: 'h'; text: string }
  | { t: 'ul' | 'ol'; items: string[]; start?: number }
  | { t: 'quote'; lines: string[] };

const FENCE = /^\s{0,3}(```|~~~)\s*([\w+#.-]*)\s*$/;
const UL = /^\s{0,3}[-*•]\s+(.*)$/;
const OL = /^\s{0,3}(\d{1,6})[.)]\s+(.*)$/;
const QUOTE = /^\s{0,3}>\s?(.*)$/;
const HEADING = /^\s{0,3}#{1,6}\s+(.*?)\s*#*\s*$/;

export function parseBlocks(src: string): Block[] {
  const lines = src.replace(/\r\n?/g, '\n').split('\n');
  const blocks: Block[] = [];
  let i = 0;
  while (i < lines.length) {
    const line = lines[i];
    const fence = FENCE.exec(line);
    if (fence) {
      const marker = fence[1];
      const body: string[] = [];
      i++;
      while (i < lines.length && !new RegExp(`^\\s{0,3}${marker}\\s*$`).test(lines[i])) body.push(lines[i++]);
      i++; // closing fence (or end of input)
      blocks.push({ t: 'code', lang: fence[2] ?? '', text: body.join('\n') });
      continue;
    }
    if (!line.trim()) {
      i++;
      continue;
    }
    const h = HEADING.exec(line);
    if (h) {
      blocks.push({ t: 'h', text: h[1] });
      i++;
      continue;
    }
    if (UL.test(line)) {
      const items: string[] = [];
      while (i < lines.length && UL.test(lines[i])) {
        let item = UL.exec(lines[i])![1];
        i++;
        while (i < lines.length && /^\s{2,}\S/.test(lines[i]) && !UL.test(lines[i]) && !OL.test(lines[i])) item += '\n' + lines[i++].trim();
        items.push(item);
      }
      blocks.push({ t: 'ul', items });
      continue;
    }
    if (OL.test(line)) {
      const items: string[] = [];
      const start = Number(OL.exec(line)![1]);
      while (i < lines.length && OL.test(lines[i])) {
        let item = OL.exec(lines[i])![2];
        i++;
        while (i < lines.length && /^\s{2,}\S/.test(lines[i]) && !UL.test(lines[i]) && !OL.test(lines[i])) item += '\n' + lines[i++].trim();
        items.push(item);
      }
      blocks.push({ t: 'ol', items, start });
      continue;
    }
    if (QUOTE.test(line)) {
      const q: string[] = [];
      while (i < lines.length && QUOTE.test(lines[i])) q.push(QUOTE.exec(lines[i++])![1]);
      blocks.push({ t: 'quote', lines: q });
      continue;
    }
    const para: string[] = [];
    while (
      i < lines.length &&
      lines[i].trim() &&
      !FENCE.test(lines[i]) &&
      !UL.test(lines[i]) &&
      !OL.test(lines[i]) &&
      !QUOTE.test(lines[i]) &&
      !HEADING.test(lines[i])
    ) {
      para.push(lines[i++]);
    }
    blocks.push({ t: 'p', lines: para });
  }
  return blocks;
}

/** Renders inline markdown in already-unescaped text; returns safe HTML. */
export function renderInline(text: string, opts: RenderOptions = {}): string {
  // Split out code spans first so nothing inside them is interpreted.
  const parts = text.split(/(`[^`\n]+`)/g);
  return parts
    .map((part) => {
      if (part.length > 2 && part.startsWith('`') && part.endsWith('`')) {
        return `<code>${escapeHtml(part.slice(1, -1))}</code>`;
      }
      return renderText(part, opts);
    })
    .join('');
}

function renderText(raw: string, opts: RenderOptions): string {
  const slots: string[] = [];
  const hold = (html: string) => {
    slots.push(html);
    return `\u0000${slots.length - 1}\u0000`;
  };
  let s = raw.replace(/\u0000/g, '');
  // [label](url)
  s = s.replace(/\[([^\]\n]{1,300})\]\(([^()\s]{1,2000})\)/g, (all, label: string, url: string) => {
    const href = safeHref(url);
    if (!href) return all;
    return hold(`<a href="${escapeHtml(href)}" target="_blank" rel="noopener noreferrer">${escapeHtml(label)}</a>`);
  });
  // bare links
  s = s.replace(/\bhttps?:\/\/[^\s<>"'`]+[^\s<>"'`.,;:!?)\]]/g, (url) =>
    hold(`<a href="${escapeHtml(url)}" target="_blank" rel="noopener noreferrer">${escapeHtml(url)}</a>`),
  );
  // Full commit hashes read as their short form; the whole hash is on hover.
  s = s.replace(/\b[0-9a-f]{40}(?:[0-9a-f]{24})?\b/g, (sha) => hold(`<code class="sha" title="${sha}">${sha.slice(0, 7)}</code>`));
  // mentions (structured only)
  if (opts.mentions && opts.mentions.size) {
    s = s.replace(/(^|[^A-Za-z0-9_.@-])@([A-Za-z0-9_][A-Za-z0-9_.-]*[A-Za-z0-9_]|[A-Za-z0-9_])/g, (all, pre: string, handle: string) => {
      const m = opts.mentions!.get(handle.toLowerCase());
      if (!m) return all;
      return (
        pre +
        hold(
          `<span class="mention" data-kind="${escapeHtml(m.kind)}" data-id="${escapeHtml(m.id)}">@${escapeHtml(m.label)}</span>`,
        )
      );
    });
  }
  s = escapeHtml(s);
  s = s.replace(/\*\*(?=\S)([^*]+?)(?<=\S)\*\*/g, '<strong>$1</strong>');
  s = s.replace(/__(?=\S)([^_]+?)(?<=\S)__/g, '<strong>$1</strong>');
  s = s.replace(/(^|[^*\w])\*(?=\S)([^*\n]+?)(?<=\S)\*(?![*\w])/g, '$1<em>$2</em>');
  s = s.replace(/\u0000(\d+)\u0000/g, (_, n: string) => slots[Number(n)] ?? '');
  return s;
}

export function renderMarkdown(src: string, opts: RenderOptions = {}): string {
  const out: string[] = [];
  for (const b of parseBlocks(src ?? '')) {
    switch (b.t) {
      case 'code': {
        const lang = b.lang ? ` data-lang="${escapeHtml(b.lang)}"` : '';
        out.push(`<pre${lang}><code>${escapeHtml(b.text)}</code></pre>`);
        break;
      }
      case 'h':
        out.push(`<p><strong>${renderInline(b.text, opts)}</strong></p>`);
        break;
      case 'p':
        out.push(`<p>${b.lines.map((l) => renderInline(l, opts)).join('<br>')}</p>`);
        break;
      case 'ul':
        out.push(`<ul>${b.items.map((it) => `<li>${it.split('\n').map((l) => renderInline(l, opts)).join('<br>')}</li>`).join('')}</ul>`);
        break;
      case 'ol': {
        const start = b.start && b.start !== 1 ? ` start="${b.start}"` : '';
        out.push(`<ol${start}>${b.items.map((it) => `<li>${it.split('\n').map((l) => renderInline(l, opts)).join('<br>')}</li>`).join('')}</ol>`);
        break;
      }
      case 'quote':
        out.push(`<blockquote>${renderMarkdown(b.lines.join('\n'), opts)}</blockquote>`);
        break;
    }
  }
  return out.join('');
}

/** Plain-text preview (search snippets, notifications): markdown markers removed. */
export function plainText(src: string, max = 200): string {
  const t = (src ?? '')
    .replace(/```[\s\S]*?```/g, ' [code] ')
    .replace(/`([^`]+)`/g, '$1')
    .replace(/\*\*([^*]+)\*\*/g, '$1')
    .replace(/\[([^\]]+)\]\([^)]+\)/g, '$1')
    .replace(/^\s*[-*>#]+\s*/gm, '')
    .replace(/\s+/g, ' ')
    .trim();
  return t.length > max ? t.slice(0, max - 1) + '…' : t;
}

/** Splits a search snippet whose matches are marked [like this] into parts. */
export function snippetParts(snippet: string): { text: string; match: boolean }[] {
  const out: { text: string; match: boolean }[] = [];
  const re = /\[([^\]\n]{1,80})\]/g;
  let last = 0;
  let m: RegExpExecArray | null;
  while ((m = re.exec(snippet))) {
    if (m.index > last) out.push({ text: snippet.slice(last, m.index), match: false });
    out.push({ text: m[1], match: true });
    last = m.index + m[0].length;
  }
  if (last < snippet.length) out.push({ text: snippet.slice(last), match: false });
  return out;
}
