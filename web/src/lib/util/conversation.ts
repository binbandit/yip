import type { Destination } from '../api/types.gen';
import { href } from '../router';

/** Preserve the thread as well as the message when returning to its source. */
export function conversationHref(source: Destination): string {
  return href({ name: 'room', roomId: source.roomId }, {
    msg: source.messageId,
    panel: source.threadId ? { kind: 'thread', id: source.threadId } : null,
  });
}
