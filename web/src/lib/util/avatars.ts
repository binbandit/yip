// Profile pictures: what can be uploaded, and the still frame shown in place
// of an animation when someone prefers reduced motion.

/** The formats the hub accepts (it checks the bytes too). SVG never: it can carry script. */
export const PICTURE_TYPES = ['image/png', 'image/jpeg', 'image/gif', 'image/webp'];
/** The hub's limit (hub.MaxAvatarBytes). */
export const MAX_PICTURE_BYTES = 10 << 20;
export const PICTURE_HINT = 'PNG, JPEG, GIF or WebP up to 10 MB; square fits best and GIFs animate. Saved as soon as you choose it.';

/** Why a chosen file can't be a picture, or '' when it can be uploaded. */
export function pictureProblem(file: Pick<File, 'type' | 'size'>): string {
  if (!PICTURE_TYPES.includes(file.type)) return 'Choose a PNG, JPEG, GIF or WebP picture.';
  if (file.size > MAX_PICTURE_BYTES) return 'Choose a picture under 10 MB.';
  return '';
}

// Large enough for the biggest avatar (64px) on a 3x display.
const STILL_SIZE = 192;
const stills = new Map<string, Promise<string | undefined>>();

/**
 * A still of a picture's first frame, as a blob URL (undefined when it can't
 * be made). Canvas draws an animated image's first frame, so this works for
 * GIF, animated WebP and APNG alike; still pictures come back unchanged but
 * smaller. Cached per URL: a picture's URL never changes content.
 */
export function stillFrame(url: string): Promise<string | undefined> {
  let still = stills.get(url);
  if (!still) {
    still = new Promise((resolve) => {
      const img = new Image();
      img.onload = () => {
        try {
          const scale = Math.min(1, STILL_SIZE / Math.max(img.naturalWidth, img.naturalHeight));
          const canvas = document.createElement('canvas');
          canvas.width = Math.max(1, Math.round(img.naturalWidth * scale));
          canvas.height = Math.max(1, Math.round(img.naturalHeight * scale));
          const ctx = canvas.getContext('2d');
          if (!ctx) return resolve(undefined);
          ctx.drawImage(img, 0, 0, canvas.width, canvas.height);
          canvas.toBlob((blob) => resolve(blob ? URL.createObjectURL(blob) : undefined));
        } catch {
          resolve(undefined);
        }
      };
      img.onerror = () => resolve(undefined);
      img.src = url;
    });
    stills.set(url, still);
    // A failure isn't kept: the next showing tries again.
    void still.then((src) => src || stills.delete(url));
  }
  return still;
}
