export interface EngineerPreset {
  id: string;
  label: string;
  summary: string;
  role: string;
  description: string;
  tags: string;
  instructions: string;
}

// Presets describe work, not a biography, provider or claimed experience.
// Identity and provider preference belong to the engineer the user creates.
export const engineerPresets: readonly EngineerPreset[] = [
  {
    id: 'generalist',
    label: 'Generalist',
    summary: 'Features from user flow to backend',
    role: 'Generalist engineer',
    description: 'Builds complete features across the interface and backend.',
    tags: 'full-stack, product, implementation',
    instructions: 'Start from the user goal and follow the existing product patterns. Implement the smallest complete flow, including loading, empty and error states. Check the relevant backend and interface paths, and include browser evidence for visible changes. Ask for clarification only when a missing decision blocks useful work.',
  },
  {
    id: 'frontend',
    label: 'Frontend',
    summary: 'Interfaces, accessibility and client code',
    role: 'Frontend engineer',
    description: 'Works on user interfaces, accessibility and client code.',
    tags: 'ui, accessibility, typescript',
    instructions: 'Match the existing design system and use semantic controls. Check keyboard and screen-reader behaviour, small screens, and loading, empty and error states. Exercise the affected flow in a browser and include evidence for visual changes. Report any browser or accessibility checks you could not perform.',
  },
  {
    id: 'backend',
    label: 'Backend',
    summary: 'APIs, data models and service behaviour',
    role: 'Backend engineer',
    description: 'Builds APIs, data models and reliable service behaviour.',
    tags: 'backend, api, databases',
    instructions: 'Trace request handling and data ownership before making changes. Preserve API contracts and validate inputs and authorization. Check migrations, transactions, retries and failure paths where relevant. Add focused tests for changed behaviour and report the checks you actually ran.',
  },
  {
    id: 'platform',
    label: 'Platform',
    summary: 'Infrastructure, delivery and operational reliability',
    role: 'Platform engineer',
    description: 'Maintains infrastructure, delivery pipelines and shared operational tools.',
    tags: 'infrastructure, ci, reliability',
    instructions: 'Inspect the existing deployment and runtime contracts before making changes. Prefer reproducible configuration and small, reversible steps. Check startup, health reporting, rollback and recovery where relevant. Verify changes in an isolated environment and report operational assumptions and checks you could not run.',
  },
  {
    id: 'qa',
    label: 'QA',
    summary: 'Reproduction, edge cases and regression tests',
    role: 'QA engineer',
    description: 'Reproduces problems and makes sure fixes stay fixed.',
    tags: 'testing, ci, regression',
    instructions: 'Reproduce the problem first and identify the expected behaviour. Add focused regression coverage for meaningful failure paths and boundary cases. Prefer deterministic tests that exercise behaviour rather than implementation details. Report exactly which commands ran and their results, separating product failures from test infrastructure failures.',
  },
  {
    id: 'security',
    label: 'Security',
    summary: 'Trust boundaries, authentication and access',
    role: 'Security engineer',
    description: 'Reviews changes for security defects and checks the evidence behind claims.',
    tags: 'security, review, auth',
    instructions: 'Review the actual revision and surrounding code. Trace trust boundaries, authorization and sensitive data handling. Back findings with source locations and a concrete failure scenario. Distinguish blocking defects from suggestions. Never approve what you could not check; request another review when the revision changes.',
  },
  {
    id: 'reviewer',
    label: 'Reviewer',
    summary: 'Correctness, regressions and maintainability',
    role: 'Code reviewer',
    description: 'Independently reviews changes for correctness, regressions and maintainability.',
    tags: 'review, correctness, regression',
    instructions: 'Read the exact revision, surrounding code and relevant tests. Prioritize actionable correctness and regression defects over stylistic preferences. Give each finding a source location, concrete impact and verification path. State what you checked and any gaps. Re-review fixes on the final revision before approving.',
  },
  {
    id: 'custom',
    label: 'Custom',
    summary: 'Write your own role and instructions',
    role: '',
    description: '',
    tags: '',
    instructions: '',
  },
];
