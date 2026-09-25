import { describe, expect, it } from 'vitest';
import { plainText, renderMarkdown } from '../../src/lib/util/markdown';

describe('markdown-lite', () => {
  it('escapes raw HTML everywhere', () => {
    const html = renderMarkdown('<img src=x onerror=alert(1)> **<b>x</b>** `<script>`');
    expect(html).not.toContain('<img');
    expect(html).not.toContain('<b>');
    expect(html).not.toContain('<script>');
    expect(html).toContain('&lt;img src=x onerror=alert(1)&gt;');
    expect(html).toContain('<code>&lt;script&gt;</code>');
  });

  it('escapes inside fenced code and keeps it verbatim', () => {
    const html = renderMarkdown('```go\nif a < b && c {\n  **not bold**\n}\n```');
    expect(html).toBe('<pre data-lang="go"><code>if a &lt; b &amp;&amp; c {\n  **not bold**\n}</code></pre>');
  });

  it('renders paragraphs, line breaks, bold, emphasis and inline code', () => {
    expect(renderMarkdown('one\ntwo\n\n**three** *four* `five`')).toBe(
      '<p>one<br>two</p><p><strong>three</strong> <em>four</em> <code>five</code></p>',
    );
  });

  it('shows full commit hashes in their short form, but leaves links and code alone', () => {
    const sha = '26bf3811219f88c19af535d6e4faaf5687a6e049';
    expect(renderMarkdown(`passes on ${sha}.`)).toBe(`<p>passes on <code class="sha" title="${sha}">26bf381</code>.</p>`);
    expect(renderMarkdown(`\`${sha}\``)).toBe(`<p><code>${sha}</code></p>`);
    expect(renderMarkdown(`https://example.com/commit/${sha}`)).toContain(`>https://example.com/commit/${sha}</a>`);
    expect(renderMarkdown('not a hash: 26bf3811219f')).toBe('<p>not a hash: 26bf3811219f</p>');
  });

  it('renders lists and quotes', () => {
    expect(renderMarkdown('- a\n- b')).toBe('<ul><li>a</li><li>b</li></ul>');
    expect(renderMarkdown('3. c\n4. d')).toBe('<ol start="3"><li>c</li><li>d</li></ol>');
    expect(renderMarkdown('> quoted')).toBe('<blockquote><p>quoted</p></blockquote>');
  });

  it('only links http, https and mailto', () => {
    expect(renderMarkdown('[ok](https://example.com/a?b=1&c=2)')).toContain(
      '<a href="https://example.com/a?b=1&amp;c=2" target="_blank" rel="noopener noreferrer">ok</a>',
    );
    const js = renderMarkdown('[bad](javascript:alert(1))');
    expect(js).not.toContain('<a');
    expect(renderMarkdown('see https://github.com/x/y/pull/42.')).toContain('href="https://github.com/x/y/pull/42"');
  });

  it('does not let link labels inject markup', () => {
    const html = renderMarkdown('[<img src=x>](https://e.com)');
    expect(html).not.toContain('<img');
  });

  it('highlights only structured mentions', () => {
    const mentions = new Map([['mira', { kind: 'engineer', id: 'e1', label: 'Mira' }]]);
    const html = renderMarkdown('@mira and @oren, mail me@mira.dev', { mentions });
    expect(html).toContain('<span class="mention" data-kind="engineer" data-id="e1">@Mira</span>');
    expect(html).toContain('@oren');
    expect(html).not.toContain('data-id="e2"');
    expect(html).toContain('me@mira.dev');
    expect(renderMarkdown('`@mira`', { mentions })).toBe('<p><code>@mira</code></p>');
  });

  it('turns headings into strong lines rather than page headings', () => {
    expect(renderMarkdown('## Result')).toBe('<p><strong>Result</strong></p>');
  });

  it('makes a plain preview', () => {
    expect(plainText('**Fixed** the `refresh` path\n\n- see [PR](https://x.y)')).toBe('Fixed the refresh path see PR');
  });
});
