import { describe, expect, it } from 'vitest';
import { filePath, parseUnifiedDiff } from '../../src/lib/util/diff';

const GIT = `diff --git a/session/validate.go b/session/validate.go
index 1111111..2222222 100644
--- a/session/validate.go
+++ b/session/validate.go
@@ -10,4 +10,4 @@ func Valid(t Token, now time.Time) bool {
 	if t.Revoked {
 		return false
 	}
-	return now.Before(t.ExpiresAt) || now.Equal(t.ExpiresAt)
+	return now.Before(t.ExpiresAt)
diff --git a/session/validate_test.go b/session/validate_test.go
new file mode 100644
--- /dev/null
+++ b/session/validate_test.go
@@ -0,0 +1,2 @@
+package session
+--- not a header, just content
`;

describe('unified diff parser', () => {
  it('splits files and counts additions and deletions', () => {
    const files = parseUnifiedDiff(GIT);
    expect(files.map(filePath)).toEqual(['session/validate.go', 'session/validate_test.go']);
    expect(files[0].additions).toBe(1);
    expect(files[0].deletions).toBe(1);
    expect(files[1].status).toBe('added');
    expect(files[1].additions).toBe(2);
  });

  it('numbers lines from the hunk header', () => {
    const [f] = parseUnifiedDiff(GIT);
    const del = f.hunks[0].lines.find((l) => l.kind === 'del')!;
    const add = f.hunks[0].lines.find((l) => l.kind === 'add')!;
    expect(del.oldNo).toBe(13);
    expect(add.newNo).toBe(13);
  });

  it('handles plain unified diffs and empty input', () => {
    const plain = '--- a.txt\n+++ b.txt\n@@ -1 +1 @@\n-x\n+y\n';
    const files = parseUnifiedDiff(plain);
    expect(files).toHaveLength(1);
    expect(files[0].newPath).toBe('b.txt');
    expect(parseUnifiedDiff('')).toEqual([]);
  });
});
