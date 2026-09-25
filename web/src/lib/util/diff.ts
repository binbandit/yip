// Parses a unified diff (git diff output) into files, hunks and lines for the
// evidence view. Anything unrecognised is skipped rather than guessed at.

export type DiffLineKind = 'add' | 'del' | 'ctx' | 'meta';

export interface DiffLine {
  kind: DiffLineKind;
  text: string;
  oldNo?: number;
  newNo?: number;
}

export interface DiffHunk {
  header: string;
  lines: DiffLine[];
}

export interface DiffFile {
  oldPath: string;
  newPath: string;
  status: 'modified' | 'added' | 'deleted' | 'renamed' | 'binary';
  additions: number;
  deletions: number;
  hunks: DiffHunk[];
}

const HUNK = /^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@(.*)$/;

const strip = (p: string) => p.replace(/\t.*$/, '').replace(/^[ab]\//, '').trim();

export function parseUnifiedDiff(text: string): DiffFile[] {
  const lines = text.replace(/\r\n?/g, '\n').split('\n');
  const files: DiffFile[] = [];
  let file: DiffFile | null = null;
  let hunk: DiffHunk | null = null;
  let oldNo = 0;
  let newNo = 0;
  let fromGitHeader = false;

  const begin = (oldPath: string, newPath: string, git: boolean): DiffFile => {
    const f: DiffFile = { oldPath, newPath, status: 'modified', additions: 0, deletions: 0, hunks: [] };
    files.push(f);
    hunk = null;
    fromGitHeader = git;
    return f;
  };

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    if (line.startsWith('diff --git ')) {
      const m = /^diff --git a\/(.+?) b\/(.+)$/.exec(line);
      file = begin(m ? m[1] : '', m ? m[2] : '', true);
      continue;
    }
    // "--- a" + "+++ b" is a file header: for plain diffs it starts a file,
    // for git diffs it refines the paths before the first hunk.
    if (line.startsWith('--- ') && (lines[i + 1] ?? '').startsWith('+++ ') && (!hunk || !fromGitHeader)) {
      const oldP = line.slice(4);
      const newP = lines[i + 1].slice(4);
      if (!file || hunk || !fromGitHeader) file = begin('', '', false);
      if (oldP.trim() === '/dev/null') file.status = 'added';
      else file.oldPath = strip(oldP);
      if (newP.trim() === '/dev/null') file.status = 'deleted';
      else file.newPath = strip(newP);
      i++;
      continue;
    }
    if (!file) continue;
    if (!hunk) {
      if (line.startsWith('new file mode')) file.status = 'added';
      else if (line.startsWith('deleted file mode')) file.status = 'deleted';
      else if (line.startsWith('rename from ') || line.startsWith('rename to ')) file.status = 'renamed';
      else if (line.startsWith('Binary files ')) file.status = 'binary';
    }
    const h = HUNK.exec(line);
    if (h) {
      oldNo = Number(h[1]);
      newNo = Number(h[2]);
      hunk = { header: line, lines: [] };
      file.hunks.push(hunk);
      continue;
    }
    if (!hunk) continue;
    const hk: DiffHunk = hunk;
    if (line.startsWith('+')) {
      hk.lines.push({ kind: 'add', text: line.slice(1), newNo: newNo++ });
      file.additions++;
    } else if (line.startsWith('-')) {
      hk.lines.push({ kind: 'del', text: line.slice(1), oldNo: oldNo++ });
      file.deletions++;
    } else if (line.startsWith('\\')) {
      hk.lines.push({ kind: 'meta', text: line });
    } else if (line.startsWith(' ')) {
      hk.lines.push({ kind: 'ctx', text: line.slice(1), oldNo: oldNo++, newNo: newNo++ });
    } else if (line === '' && i < lines.length - 1) {
      hk.lines.push({ kind: 'ctx', text: '', oldNo: oldNo++, newNo: newNo++ });
    }
  }
  return files;
}

export function filePath(f: DiffFile): string {
  if (f.status === 'deleted') return f.oldPath;
  if (f.status === 'renamed' && f.oldPath && f.oldPath !== f.newPath) return `${f.oldPath} → ${f.newPath}`;
  return f.newPath || f.oldPath;
}
