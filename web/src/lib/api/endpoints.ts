// Typed wrappers for every /v1 endpoint the client uses (see docs/api.md).
import { del, get, patch, post, put, upload } from './client';
import type {
  AcceptJobRequest,
  Approval,
  ApprovalDecisionRequest,
  Bootstrap,
  CancelJobRequest,
  CleanupWorkspaceRequest,
  CreateEngineerRequest,
  CreateEnrollmentRequest,
  CreateProjectRequest,
  CreateRoomRequest,
  Decision,
  DecisionActionRequest,
  DecisionRequest,
  DiagnosticBundle,
  Diagnostics,
  Engineer,
  EngineerNote,
  EngineerVersion,
  Enrollment,
  Job,
  JobDetail,
  JobInputRequest,
  JobInputResponse,
  MembershipPreview,
  Message,
  MessagePage,
  Node,
  NoteActionRequest,
  NoteRequest,
  Overview,
  PostMessageRequest,
  PostMessageResponse,
  Preferences,
  Project,
  ProviderProfile,
  PullRequest,
  PutGrantRequest,
  PutRepoRequest,
  Question,
  RetryJobRequest,
  Review,
  Room,
  Run,
  RunActivity,
  SearchResult,
  SetupRequest,
  SetupStatus,
  UpdateEngineerRequest,
  UpdateProjectRequest,
  UpdateRoomRequest,
  WorkRow,
} from './types.gen';

const q = (v: string) => encodeURIComponent(v);

export const api = {
  // setup & session
  setupStatus: () => get<SetupStatus>('/v1/setup', { quiet401: true }),
  setup: (req: SetupRequest) => post<{ user: unknown }>('/v1/setup', req, { quiet401: true }),
  signIn: (handle: string, password: string) => post<{ ok: boolean }>('/v1/session', { handle, password }, { quiet401: true }),
  signOut: () => del<{ ok: boolean }>('/v1/session', { quiet401: true }),
  bootstrap: (opts?: { quiet401?: boolean }) => get<Bootstrap>('/v1/bootstrap', opts),
  putPreferences: (preferences: Preferences) => put<Preferences>('/v1/preferences', { preferences }),

  // rooms & messages
  rooms: () => get<Room[]>('/v1/rooms'),
  room: (id: string) => get<Room>(`/v1/rooms/${q(id)}`),
  createRoom: (req: CreateRoomRequest) => post<Room>('/v1/rooms', req),
  updateRoom: (id: string, req: UpdateRoomRequest) => patch<Room>(`/v1/rooms/${q(id)}`, req),
  previewMember: (roomId: string, engineerId: string) =>
    get<MembershipPreview>(`/v1/rooms/${q(roomId)}/members/${q(engineerId)}/preview`),
  addMember: (roomId: string, engineerId: string) => put<Room>(`/v1/rooms/${q(roomId)}/members/${q(engineerId)}`),
  removeMember: (roomId: string, engineerId: string) => del<Room>(`/v1/rooms/${q(roomId)}/members/${q(engineerId)}`),
  messages: (roomId: string, before?: number, limit = 60) =>
    get<MessagePage>(`/v1/rooms/${q(roomId)}/messages?limit=${limit}${before ? `&before=${before}` : ''}`),
  postMessage: (roomId: string, req: PostMessageRequest) => post<PostMessageResponse>(`/v1/rooms/${q(roomId)}/messages`, req),
  markRead: (roomId: string, seq: number) => post<{ ok: boolean }>(`/v1/rooms/${q(roomId)}/read`, { seq }),
  /** The room's work strip; `includeReplies` adds live conversational replies. */
  roomWork: (roomId: string, includeReplies = false) =>
    get<WorkRow[]>(`/v1/rooms/${q(roomId)}/work${includeReplies ? '?include=replies' : ''}`),
  thread: (rootId: string) => get<MessagePage>(`/v1/threads/${q(rootId)}`),
  react: (messageId: string, emoji: string, remove: boolean) =>
    post<Message>(`/v1/messages/${q(messageId)}/reactions`, { emoji, remove }),
  editMessage: (messageId: string, body: string) => patch<Message>(`/v1/messages/${q(messageId)}`, { body }),
  deleteMessage: (messageId: string) => del<{ ok: boolean }>(`/v1/messages/${q(messageId)}`),

  // engineers & projects
  engineers: () => get<Engineer[]>('/v1/engineers'),
  engineer: (id: string) => get<{ engineer: Engineer; versions: EngineerVersion[] }>(`/v1/engineers/${q(id)}`),
  createEngineer: (req: CreateEngineerRequest) => post<Engineer>('/v1/engineers', req),
  updateEngineer: (id: string, req: UpdateEngineerRequest) => patch<Engineer>(`/v1/engineers/${q(id)}`, req),
  projects: () => get<Project[]>('/v1/projects'),
  project: (id: string) => get<Project>(`/v1/projects/${q(id)}`),
  createProject: (req: CreateProjectRequest) => post<Project>('/v1/projects', req),
  updateProject: (id: string, req: UpdateProjectRequest) => patch<Project>(`/v1/projects/${q(id)}`, req),
  putRepo: (projectId: string, repoId: string | 'new', req: PutRepoRequest) =>
    put<Project>(`/v1/projects/${q(projectId)}/repos/${q(repoId)}`, req),
  importRepo: (projectId: string, file: Blob, opts: { name?: string; branch?: string; repoId?: string }) =>
    upload<Project>(
      `/v1/projects/${q(projectId)}/repos/import?name=${q(opts.name ?? '')}&branch=${q(opts.branch ?? '')}&repo=${q(opts.repoId ?? '')}`,
      file,
    ),
  putGrant: (projectId: string, engineerId: string, req: PutGrantRequest) =>
    put<Project>(`/v1/projects/${q(projectId)}/grants/${q(engineerId)}`, req),

  // work
  jobs: (params: { state?: string[]; project?: string; owner?: string } = {}) => {
    const s = new URLSearchParams();
    if (params.state?.length) s.set('state', params.state.join(','));
    if (params.project) s.set('project', params.project);
    if (params.owner) s.set('owner', params.owner);
    const qs = s.toString();
    return get<Job[]>(`/v1/jobs${qs ? `?${qs}` : ''}`);
  },
  job: (id: string) => get<JobDetail>(`/v1/jobs/${q(id)}`),
  runActivity: (jobId: string, runId: string) => get<RunActivity[]>(`/v1/jobs/${q(jobId)}/runs/${q(runId)}/activity`),
  jobInput: (jobId: string, req: JobInputRequest) => post<JobInputResponse>(`/v1/jobs/${q(jobId)}/input`, req),
  cancelJob: (jobId: string, req: CancelJobRequest) => post<Job>(`/v1/jobs/${q(jobId)}/cancel`, req),
  restartJob: (jobId: string) => post<Job>(`/v1/jobs/${q(jobId)}/restart`, {}),
  removeWorkspace: (nodeId: string, name: string, req: CleanupWorkspaceRequest) =>
    post<Node>(`/v1/nodes/${q(nodeId)}/workspaces/${q(name)}/remove`, req),
  retryJob: (jobId: string, req: RetryJobRequest) => post<Job>(`/v1/jobs/${q(jobId)}/retry`, req),
  acceptJob: (jobId: string, req: AcceptJobRequest) => post<Job>(`/v1/jobs/${q(jobId)}/accept`, req),
  /** Attempts queued or executing in your rooms (for "working" indicators). */
  runs: () => get<Run[]>('/v1/runs'),
  question: (id: string) => get<Question>(`/v1/questions/${q(id)}`),
  review: (id: string) => get<Review>(`/v1/reviews/${q(id)}`),
  pullRequest: (id: string, refresh = false) => get<PullRequest>(`/v1/pull-requests/${q(id)}${refresh ? '?refresh=1' : ''}`),
  approval: (id: string) => get<Approval>(`/v1/approvals/${q(id)}`),
  decideApproval: (id: string, req: ApprovalDecisionRequest) => post<Approval>(`/v1/approvals/${q(id)}/decision`, req),
  answerQuestion: (id: string, body: string, clientKey: string) =>
    post<PostMessageResponse>(`/v1/questions/${q(id)}/answer`, { body, clientKey }),

  // machines
  nodes: () => get<Node[]>('/v1/nodes'),
  createEnrollment: (req: CreateEnrollmentRequest) => post<Enrollment>('/v1/nodes/enrollments', req),
  drainNode: (id: string, drain: boolean) => post<Node>(`/v1/nodes/${q(id)}/drain`, { drain }),
  probeNode: (id: string) => post<{ ok: boolean }>(`/v1/nodes/${q(id)}/probe`, {}),
  trustNodeRules: (id: string, provider: string, trust: boolean) => post<Node>(`/v1/nodes/${q(id)}/trust-rules`, { provider, trust }),
  providerProfiles: () => get<ProviderProfile[]>('/v1/provider-profiles'),
  setProviderConcurrency: (id: string, maxConcurrency: number) => put<ProviderProfile>(`/v1/provider-profiles/${q(id)}`, { maxConcurrency }),
  stopNodeWork: (id: string) => post<{ ok: boolean }>(`/v1/nodes/${q(id)}/stop`),
  revokeNode: (id: string) => del<{ ok: boolean }>(`/v1/nodes/${q(id)}/credential`),

  // knowledge, overview, search
  decisions: (status?: string) => get<Decision[]>(`/v1/decisions${status ? `?status=${q(status)}` : ''}`),
  decision: (id: string) => get<Decision>(`/v1/decisions/${q(id)}`),
  decideDecision: (id: string, req: DecisionActionRequest) => post<Decision>(`/v1/decisions/${q(id)}`, req),
  engineerNotes: (engineerId: string) => get<EngineerNote[]>(`/v1/engineers/${q(engineerId)}/notes`),
  createNote: (engineerId: string, req: NoteRequest) => post<EngineerNote>(`/v1/engineers/${q(engineerId)}/notes`, req),
  decideNote: (id: string, req: NoteActionRequest) => post<EngineerNote>(`/v1/notes/${q(id)}`, req),
  createDecision: (req: DecisionRequest) => post<Decision>('/v1/decisions', req),
  overview: (since?: string) => get<Overview>(`/v1/overview${since ? `?since=${q(since)}` : ''}`),
  /** Records the visit that "Since you were here" is measured from. */
  overviewSeen: () => post<{ ok: boolean }>('/v1/overview/seen'),
  search: (query: string, scope: { room?: string; project?: string } = {}, signal?: AbortSignal) =>
    get<SearchResult[]>(`/v1/search?q=${q(query)}${scope.room ? `&room=${q(scope.room)}` : ''}${scope.project ? `&project=${q(scope.project)}` : ''}`, {
      signal,
    }),
  diagnostics: () => get<Diagnostics>('/v1/diagnostics'),
  diagnosticBundle: () => get<DiagnosticBundle>('/v1/diagnostics/bundle'),
};

export const artifactUrl = (id: string, download = false) => `/v1/artifacts/${q(id)}${download ? '?download=1' : ''}`;
export const exportUrl = '/v1/export';

export async function fetchArtifactText(id: string, maxBytes = 2_000_000): Promise<{ text: string; truncated: boolean }> {
  const res = await fetch(artifactUrl(id), { credentials: 'same-origin' });
  if (!res.ok) throw new Error(res.status === 404 ? 'That file is no longer available.' : 'The file could not be loaded.');
  const text = await res.text();
  if (text.length > maxBytes) return { text: text.slice(0, maxBytes), truncated: true };
  return { text, truncated: false };
}
