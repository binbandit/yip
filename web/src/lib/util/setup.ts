import type { Engineer, Project, Room } from '../api/types.gen';
import type { DataState } from '../state/data';
import { providerReadiness } from './providerReadiness';

type SetupData = Pick<DataState, 'demo' | 'nodes' | 'engineers' | 'rooms' | 'projects' | 'jobs'>;

export function roomSettings(room: Room): string {
  return `/rooms/${room.id}?panel=room%3A${room.id}`;
}

/** Follow one connected team, rather than counting unrelated setup objects. */
export function setupReadiness(data: SetupData) {
  const nodes = Object.values(data.nodes).filter((n) => !n.revokedAt && n.status !== 'revoked');
  const allowedProvider = (provider: string) => !!provider && (provider !== 'fake' || data.demo);
  const engineers = Object.values(data.engineers).filter((e) => !e.archived && allowedProvider(e.provider.provider));
  const rooms = Object.values(data.rooms).filter((r) => r.kind === 'room' && !r.archived);
  const projects = Object.values(data.projects);
  const member = (e: Engineer, r: Room) => r.members.some((m) => m.kind === 'engineer' && m.id === e.id);
  const linked = (p: Project, r: Room) => r.projectIds.includes(p.id) || p.roomIds.includes(r.id);
  const access = (e: Engineer, p: Project) => p.grants.some((g) => g.engineerId === e.id && (g.access === 'read' || g.access === 'write'));
  const signedIn = (e: Engineer) => providerReadiness(nodes, e.provider).signedIn.length > 0;
  const available = (e: Engineer) => providerReadiness(nodes, e.provider).ready.length > 0;

  // Connectivity wins over incidental order or a different provider being online.
  const fallbackProject = projects.find((p) => p.repos.length) ?? projects[0];
  const candidates = engineers.flatMap((engineer) => {
    const joined = rooms.filter((r) => member(engineer, r));
    return (joined.length ? joined : [rooms[0]]).flatMap((room) => {
      const linkedProjects = room ? projects.filter((p) => linked(p, room)) : [];
      return (linkedProjects.length ? linkedProjects : [fallbackProject]).map((project) => {
        const inRoom = !!room && member(engineer, room);
        const projectLinked = !!room && !!project && linked(project, room);
        const projectReady = inRoom && projectLinked && !!project?.repos.length && access(engineer, project);
        const reviewers = room && project ? engineers.filter((e) => e.id !== engineer.id && member(e, room) && access(e, project)) : [];
        const score = Number(projectReady) * 32 + Number(inRoom) * 16 + Number(projectLinked) * 8 +
          Number(!!project?.repos.length) * 4 + Number(reviewers.length > 0) * 2 + Number(signedIn(engineer));
        return { engineer, room, project, inRoom, projectLinked, projectReady, reviewers, score };
      });
    });
  });
  const target = candidates.sort((a, b) => b.score - a.score)[0];
  const engineer = target?.engineer;
  const room = target?.room ?? rooms[0];
  const project = target?.project ?? projects[0];
  const reviewers = target?.reviewers ?? [];
  const reviewer = reviewers.find(available) ?? reviewers[0];
  const reviewerCandidate = engineers.find((e) => e.id !== engineer?.id && !!room && member(e, room)) ?? engineers.find((e) => e.id !== engineer?.id);
  const needsReviewer = project?.policy.requirePeerReview ?? true;
  const providerInstalled = nodes.flatMap((n) => n.providers ?? []).filter((p) => allowedProvider(p.provider));
  const providerSignedIn = engineer ? signedIn(engineer) : providerInstalled.some((p) => p.authState === 'ready');
  const completed = Object.values(data.jobs).some((j) => j.state === 'completed' && j.kind !== 'reply' && j.kind !== 'review' && !j.parentId);
  const unavailable = [engineer, ...(needsReviewer ? [reviewer] : [])].flatMap((e) => {
    if (!e) return [];
    const readiness = providerReadiness(nodes, e.provider);
    if (readiness.ready.length) return [];
    const hasConnectedSignIn = readiness.signedIn.some(({ node }) => node.status === 'online' && !node.draining);
    const needsProviderChoice = hasConnectedSignIn || (!readiness.signedIn.length && !!e.provider.profileId);
    return [{
      engineer: e,
      reason: readiness.reason,
      href: needsProviderChoice ? `/engineers/${e.id}#eng-prov` : '/machines',
      action: needsProviderChoice ? 'Review provider settings' : 'Check machines',
    }];
  });
  return {
    nodes, engineer, room, project, reviewer, reviewerCandidate, needsReviewer, providerSignedIn, providerInstalled,
    inRoom: target?.inRoom ?? false,
    projectLinked: target?.projectLinked ?? false,
    projectReady: target?.projectReady ?? false,
    reviewReady: !needsReviewer || !!reviewer,
    completed,
    unavailable,
    readyNow: !!target?.projectReady && (!needsReviewer || !!reviewer) && unavailable.length === 0,
  };
}
