<script lang="ts">
  import { untrack } from 'svelte';
  import { app } from '../lib/state/app.svelte';
  import { api } from '../lib/api/endpoints';
  import { ApiError } from '../lib/api/client';
  import { mergeRoom } from '../lib/state/data';
  import type { Room } from '../lib/api/types.gen';
  import ConfirmDialog from './ConfirmDialog.svelte';

  let { room, onclose }: { room: Room; onclose: () => void } = $props();
  let snapshot = $state(untrack(() => room));
  const data = app.data;
  const location = app.loc;
  let alive = true;
  $effect(() => () => { alive = false; });

  async function archive() {
    try {
      const updated = await api.updateRoom(snapshot.id, { version: snapshot.version, archived: true });
      if (!alive || app.data !== data) return;
      mergeRoom(data, updated);
      app.announce(`${updated.name} archived. Ongoing work continues.`);
      if (app.loc === location && location.route.name === 'room' && location.route.roomId === updated.id) {
        app.navigate(app.homePath(), { replace: true });
      }
    } catch (err) {
      if (err instanceof ApiError && err.conflict && alive && app.data === data) {
        const latest = await api.room(snapshot.id);
        if (alive && app.data === data) {
          mergeRoom(data, latest);
          snapshot = latest;
        }
        throw new Error('This room changed. It has not been archived by this action. Review the room and try again.');
      }
      throw err;
    }
  }
</script>

<ConfirmDialog
  title="Archive {snapshot.name}?"
  body="The room leaves the sidebar for its members. Its history, work and decisions are kept and still searchable. Ongoing work continues; archiving does not cancel it."
  confirmLabel="Archive"
  danger
  onconfirm={archive}
  onclose={() => { if (alive) onclose(); }}
/>
