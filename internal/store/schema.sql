-- agentwork schema. Run once at startup (CREATE TABLE IF NOT EXISTS).
-- Single-user, single-file SQLite (WAL mode).
-- foreign_keys + journal_mode are set per-connection via the DSN in store.go
-- (they are per-connection pragmas, so setting them here only affects the one
-- connection that runs this file). Kept here for visibility/documentation.
--
-- v2 (DESIGN.md): adds the domain layer (asset/evolution domain owning
-- acceptance policy + gates) and the review checkpoint state. M0 has no
-- migration tooling: the DB may be wiped and rebuilt (data is disposable).

PRAGMA journal_mode = WAL;
PRAGMA foreign_keys = ON;

-- Two-layer coordination model (see DESIGN.md §2):
--   domain = an asset/evolution domain (shared repo + acceptance policy).
--   goal   = a work item (product plane). The SOLE holder of state authority.
--   run    = one execution of a goal by one agent (execution plane). No
--            authority — on terminal status it reports up to the goal layer
--            via GoalService.reconcileOnRunEnd, which is the only place that
--            changes goal.status.
-- A goal can be executed many times / by many agents in succession; full
-- history is retained across runs.

-- domain: an asset/evolution domain. Owns the shared repo, the acceptance
-- policy (NL intent + compiled checks), and default gates. M0 implements
-- type=repo only; other asset types (docs/infra-config/knowledge/backlog)
-- are deferred (DESIGN.md §13). The acceptance policy is defined by the
-- domain owner in natural language (policy_text), compiled by the processor
-- agent into executable checks (checks), and frozen after user confirmation
-- (checks_compiled_at). See DESIGN.md §5 (triangle separation).
CREATE TABLE IF NOT EXISTS domain (
    id                     TEXT PRIMARY KEY,
    type                   TEXT NOT NULL DEFAULT 'repo', -- repo（共享仓+worktree）| scratch（无仓库：持久项目目录 runs/scratch/<name>/goals/<goalID>，交付物=汇报；人卡点强制）
    name                   TEXT NOT NULL UNIQUE,
    git_url                TEXT NOT NULL DEFAULT '',     -- shared repo source
    default_branch         TEXT NOT NULL DEFAULT 'main',
    git_identity           TEXT NOT NULL DEFAULT '',     -- "name <email>" for commits
    git_credentials        TEXT NOT NULL DEFAULT '',     -- token/ssh ref; M0 single-user shared
    policy_text            TEXT NOT NULL DEFAULT '',     -- NL intent (source of truth)
    checks                 TEXT NOT NULL DEFAULT '[]',   -- compiled: verify cmds + guards + gates (JSON)
    verification_strength  TEXT NOT NULL DEFAULT 'medium', -- strong|medium|weak (processor-inferred, user-confirmed)
    max_run_duration       INTEGER NOT NULL DEFAULT 7200,  -- seconds per run; 0 = unlimited
    verify_timeout         INTEGER NOT NULL DEFAULT 600,   -- seconds per verify command
    processor_agent_id     TEXT NOT NULL DEFAULT '',       -- per-domain override of the global processor agent
    checks_compiled_at     TEXT NOT NULL DEFAULT '',       -- '' = not compiled yet
    metrics_baseline       TEXT NOT NULL DEFAULT '{}',     -- JSON: test count / coverage at creation
    issue_repo             TEXT NOT NULL DEFAULT '',       -- M4-B: "owner/repo" to track issues from ('' = none)
    issue_assignee         TEXT NOT NULL DEFAULT '',       -- M4-B: agent|squad id that handles this repo's issues ('' = don't auto-create)
    issue_assignee_type    TEXT NOT NULL DEFAULT 'agent',  -- M4-B: agent | squad (what issue_assignee points at)
    issue_provider         TEXT NOT NULL DEFAULT 'github', -- M4-B: github | gitcode (issue API + webhook signature shape)
    pull_assignee          TEXT NOT NULL DEFAULT '',       -- M4-B PR pull: agent|squad id that handles this repo's pull requests ('' = don't pull PRs)
    pull_assignee_type     TEXT NOT NULL DEFAULT 'agent',  -- M4-B PR pull: agent | squad (what pull_assignee points at)
    webhook_secret         TEXT NOT NULL DEFAULT '',        -- repo-level webhook secret ('' = use platform.webhook_secret)
    created_at             TEXT NOT NULL
);

-- webhook_delivery: one row per inbound webhook delivery (including rejected
-- ones) — the ground truth for "is the repo's webhook actually reaching us?",
-- independent of whether the host token can list hooks. domain_id is '' when
-- the delivery's repo is not tracked. ok=1 means the signature verified and
-- the payload was handled (untracked / no-handler is still ok for the
-- platform — it is not an error). Trimmed to the newest 500 rows on insert.
CREATE TABLE IF NOT EXISTS webhook_delivery (
    id          TEXT PRIMARY KEY,
    provider    TEXT NOT NULL DEFAULT '',       -- github | gitcode
    repo        TEXT NOT NULL DEFAULT '',       -- owner/repo ('' when unparseable)
    domain_id   TEXT NOT NULL DEFAULT '',       -- matched domain ('' = untracked repo)
    event       TEXT NOT NULL DEFAULT '',       -- issues | pull_request | ''
    action      TEXT NOT NULL DEFAULT '',       -- opened / reopened / synchronize / ...
    ok          INTEGER NOT NULL DEFAULT 0,     -- 1 = handled (signature + parse passed)
    error       TEXT NOT NULL DEFAULT '',
    received_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_webhook_delivery_domain ON webhook_delivery(domain_id, received_at);
CREATE INDEX IF NOT EXISTS idx_webhook_delivery_repo ON webhook_delivery(repo, received_at);

-- machine: one remote machine running `agentwork connect` — the execution
-- host registered to the platform (CLI 分支 Phase 1). The probed agent
-- CLIs ride probed_clis (JSON); they become runtime rows once remote
-- execution lands (Phase 2). status flips offline via the server's stale
-- sweep when heartbeats stop.
CREATE TABLE IF NOT EXISTS machine (
    id           TEXT PRIMARY KEY,
    name         TEXT NOT NULL DEFAULT '',
    hostname     TEXT NOT NULL DEFAULT '',
    version      TEXT NOT NULL DEFAULT '',
    probed_clis  TEXT NOT NULL DEFAULT '[]', -- JSON []link.ProbeCLI
    last_seen_at TEXT NOT NULL DEFAULT '',
    status       TEXT NOT NULL DEFAULT 'connected', -- connected|offline
    created_at   TEXT NOT NULL
);

-- A runtime is the launch spec of one probed agent CLI on one registered
-- machine (CLI 分支): id + machine + how the machine spawns it (args =
-- acp_spawn) + env. The local-transport concepts
-- (transport/provider/executable/endpoint/agentwork_url) are RETIRED — the
-- daemon never opens a transport; every run dispatches over the machine's
-- /connect link. The wire protocol is the MACHINE's implementation detail
-- (always ACP today; a future a2a backend would live in the machine's
-- executor, not as a platform column).
CREATE TABLE IF NOT EXISTS runtime (
    id            TEXT PRIMARY KEY,
    name          TEXT NOT NULL UNIQUE,        -- <cli>@<machine> (probe-generated)
    machine_id    TEXT NOT NULL DEFAULT '',    -- the registered machine that executes this runtime's runs
    args          TEXT NOT NULL DEFAULT '[]',  -- acp_spawn: how the machine starts the CLI
    env           TEXT NOT NULL DEFAULT '{}',  -- runtime env (layered over the machine env at spawn)
    -- status: active | absent — absent = the machine's latest probe no
    -- longer sees this CLI (uninstalled). The row survives (agents
    -- reference it) but the claim gate rejects it.
    status        TEXT NOT NULL DEFAULT 'active',
    created_at    TEXT NOT NULL                -- RFC3339
);

-- An agent is a runtime + a persona. Creating an agent does NOT launch a
-- process under the per-task model: a fresh connection is opened per run.
-- agent.status/pid columns are deliberately absent here — they belong to the
-- future long-lived-session model and would be dead columns today.
-- workdir_base was removed in v2: where a run works is decided by the goal's
-- domain (worktree), never by the agent (DESIGN.md §6).
-- type: standard (user-created, the default) | steward (system-internal
-- agent for team-import / intake; auto-seeded at daemon startup).
CREATE TABLE IF NOT EXISTS agent (
    id              TEXT PRIMARY KEY,
    name            TEXT NOT NULL UNIQUE,
    type            TEXT NOT NULL DEFAULT 'standard',  -- standard | steward
    description     TEXT NOT NULL DEFAULT '',  -- human-facing one-liner (shown in the web list); distinct from system_prompt (the persona)
    runtime_id      TEXT NOT NULL REFERENCES runtime(id),
    system_prompt   TEXT NOT NULL DEFAULT '',
    model           TEXT NOT NULL DEFAULT '',  -- optional override
    env             TEXT NOT NULL DEFAULT '{}', -- agent-level env, layered over runtime env
    mcp_servers     TEXT NOT NULL DEFAULT '[]', -- extra MCP servers advertised at session/new (acp.McpServer JSON array); the platform's workspace server is always prepended
    skills          TEXT NOT NULL DEFAULT '[]', -- JSON []skill-id — platform-managed skills pushed to the agent's machine (CLI 分支 Phase 4)
    max_concurrent  INTEGER NOT NULL DEFAULT 1,
    created_at      TEXT NOT NULL
);

-- skill: a platform-managed skill package (SKILL.md + resources) — the
-- skills library agents get their skills from. Files live on disk under
-- the skills root (<runsRoot>/skills/<id>/); the machine receives them
-- via config.push and installs them under agentwork-<name>/ (CLI 分支
-- Phase 4).
CREATE TABLE IF NOT EXISTS skill (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL,
    -- Remote repository binding (feature: remote skill source). Empty
    -- repo_url = the skill is local-only. repo_token is the git platform
    -- token used for clone/push/PR; never returned by the API.
    repo_url         TEXT NOT NULL DEFAULT '',
    repo_provider    TEXT NOT NULL DEFAULT '',  -- github | gitcode
    repo_branch      TEXT NOT NULL DEFAULT '',  -- remote branch; '' = main
    repo_path        TEXT NOT NULL DEFAULT '',  -- subdirectory holding this skill in the repo
    repo_token       TEXT NOT NULL DEFAULT '',
    last_synced_at   TEXT NOT NULL DEFAULT '',
    last_sync_action TEXT NOT NULL DEFAULT '',  -- pulled | pushed | pr
    last_remote_sha  TEXT NOT NULL DEFAULT ''
);

-- A squad is a routing group. It does no work itself: assigning a goal to a
-- squad or @mentioning a squad routes only to its leader agent, who then
-- splits work into sub-goals. Designed per multica §7 (no member fan-out).
-- The leader must be an agent; squads cannot nest.
CREATE TABLE IF NOT EXISTS squad (
    id           TEXT PRIMARY KEY,
    name         TEXT NOT NULL UNIQUE,
    description  TEXT NOT NULL DEFAULT '',
    leader_id    TEXT NOT NULL REFERENCES agent(id),  -- must be an agent
    instructions TEXT NOT NULL DEFAULT '',            -- extra briefing for leader runs
    created_at   TEXT NOT NULL
);

-- squad_member is polymorphic: member_type discriminates agent vs human.
-- member_id has no single FK constraint (the discriminator is the CHECK);
-- the application resolves the relationship. No nesting ('squad' not allowed).
CREATE TABLE IF NOT EXISTS squad_member (
    id          TEXT PRIMARY KEY,
    squad_id    TEXT NOT NULL REFERENCES squad(id) ON DELETE CASCADE,
    member_type TEXT NOT NULL CHECK (member_type IN ('agent', 'human')),
    member_id  TEXT NOT NULL,
    role        TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL,
    UNIQUE(squad_id, member_type, member_id)
);

-- goal: a work item (product plane). The sole holder of state authority.
-- assignee is polymorphic (agent | squad | human).
-- domain_id: agent-executed goals must belong to a domain — the domain owns
-- the worktree and the acceptance policy (DESIGN.md §2/§5).
-- status 'review' (v2): the goal is parked waiting for a human checkpoint
-- decision.
CREATE TABLE IF NOT EXISTS goal (
    id               TEXT PRIMARY KEY,
    title            TEXT NOT NULL,
    description      TEXT NOT NULL DEFAULT '',
    domain_id        TEXT REFERENCES domain(id),      -- owning domain (required for agent goals)
    assignee_type    TEXT NOT NULL DEFAULT 'agent',   -- agent | squad | human
    assignee_id      TEXT NOT NULL DEFAULT '',
    status           TEXT NOT NULL DEFAULT 'backlog', -- backlog|active|done|failed|review|cancelled
    category         TEXT NOT NULL DEFAULT 'hitl',    -- hitl (human checkpoint) | hootl (finish without human review)
    handoff_note     TEXT NOT NULL DEFAULT '',
    review_request   TEXT NOT NULL DEFAULT '',        -- gate trigger reason / evidence pointer
    human_iterations INTEGER NOT NULL DEFAULT 0,      -- human reject iterations (separate from run.attempt)
    created_by_type  TEXT NOT NULL DEFAULT 'human',   -- human | agent
    created_by_id    TEXT NOT NULL DEFAULT '',
    created_at       TEXT NOT NULL,
    source_ref       TEXT NOT NULL DEFAULT '',     -- external source (M4-B): "github:owner/repo#123" — one goal per issue
    connection_id    TEXT NOT NULL DEFAULT '',     -- repo connection that ingested this goal ('' = legacy repo-level binding)
    execution_attempt INTEGER NOT NULL DEFAULT 0,  -- v2 (决策 6-9): machine-retry counter, authoritative; run.attempt is just the instance ordinal
    attention        TEXT NOT NULL DEFAULT '',     -- v2 (决策 6-8): derived OwnerAttention persisted by Reconcile ('' | integration|wrapup|recovery|user_action)
    execution_mode   TEXT NOT NULL DEFAULT 'infer', -- RSI: infer | train (train freezes the workflow graph at terminal and analyzes it)
    content_capture  TEXT NOT NULL DEFAULT 'redacted', -- RSI: none | redacted | full
    budget_usd       REAL NOT NULL DEFAULT 2,      -- RSI: analysis budget ceiling (displayed/enforced later)
    sample_rate      REAL NOT NULL DEFAULT 1       -- RSI: 0..1 trace sampling rate
);

CREATE INDEX IF NOT EXISTS idx_goal_assignee ON goal(assignee_type, assignee_id);
CREATE INDEX IF NOT EXISTS idx_goal_status ON goal(status);

-- run: one execution of a goal by one agent (execution plane). No authority:
-- on terminal status the daemon calls GoalService.reconcileOnRunEnd(run),
-- which checks whether this run still belongs to the current assignee before
-- touching goal.status. status here is the execution-plane state machine.
-- run_kind 'processor' marks platform-internal runs (e.g. the NL→checks
-- compiler run) — same scheduling mechanism, no goal (DESIGN.md §8).
-- run_type subdivides processor runs by task (M3): compile (NL→checks) and
-- intake (IM natural-language command parsing). The daemon's
-- runProcessorTask dispatches on it; more processor task types (digest
-- generation, health analysis — DESIGN.md §13) extend this enum.
-- evidence: the gate evidence bundle (diff stats + verify output + summary).
CREATE TABLE IF NOT EXISTS run (
    id                 TEXT PRIMARY KEY,
    goal_id            TEXT NOT NULL DEFAULT '',     -- '' for processor runs (no goal)
    agent_id           TEXT NOT NULL REFERENCES agent(id),
    run_kind           TEXT NOT NULL DEFAULT 'worker', -- worker|processor
    run_type           TEXT NOT NULL DEFAULT '',  -- processor tasks: compile|intake (M3)
    domain_id          TEXT NOT NULL DEFAULT '',  -- processor runs: the domain being processed (compile target)
    gates_hit          TEXT NOT NULL DEFAULT '[]',-- M2: JSON []string — gate rules this run's outcome triggered
    prompt             TEXT NOT NULL DEFAULT '',  -- platform-internal runs only (processor): the compile/processing instruction
    session_id         TEXT NOT NULL DEFAULT '',  -- protocol-returned; for history/future resume
    workdir            TEXT NOT NULL DEFAULT '',
    -- CLI 分支: the machine this run was dispatched to — stamped at dispatch,
    -- the anchor every /connect report (claimed/events/finished) is checked
    -- against. '' = never dispatched.
    machine_id         TEXT NOT NULL DEFAULT '',
    status             TEXT NOT NULL DEFAULT 'queued', -- queued|running|completed|failed|cancelled
    cancel_reason      TEXT NOT NULL DEFAULT '',  -- structured: idle_watchdog|handoff|stopped|timeout|runaway|goal_terminal|goal_cancelled|'' (decisions don't string-match summaries)
    attempt            INTEGER NOT NULL DEFAULT 1,
    result_summary     TEXT NOT NULL DEFAULT '',
    evidence           TEXT NOT NULL DEFAULT '',   -- JSON: diff stats + verify output + agent summary
    trigger_comment_id TEXT NOT NULL DEFAULT '',  -- which comment caused this run (mention/child-done)
    is_leader_run      INTEGER NOT NULL DEFAULT 0,-- 1 if a squad leader run
    squad_id           TEXT NOT NULL DEFAULT '',  -- the squad a leader run belongs to
    role               TEXT NOT NULL DEFAULT 'owner', -- owner|subgoal|consult|review|verify ('' for processor runs); informational snapshot, authority stays dynamic at reconcile
    wake_note          TEXT NOT NULL DEFAULT '',  -- owner spawns: WHY this run was woken, compiled in the spawn transaction (决策 6-17) — the prompt reads the run row, not the mutable goal.attention
    wake_anchor        TEXT NOT NULL DEFAULT '',  -- the comment the wake refers to (决策 6-22: the get_comments(after=) handle; '' = no comment anchor)
    sub_goal_id        TEXT NOT NULL DEFAULT '',  -- v2 (决策 6-9): the sub-goal this run executes (1:N with sub_goal; '' for goal-level runs)
    base_ref           TEXT NOT NULL DEFAULT '',  -- subgoal runs: merge-base(goal branch, sub-goal branch) at run end — the Change revision's integration base
    head_ref           TEXT NOT NULL DEFAULT '',  -- subgoal runs: the branch head SHA the Change revision delivers
    dirty_snapshot     TEXT NOT NULL DEFAULT '',  -- retired (run-scoped workspaces, 决策 6-2); kept for now
    token              TEXT NOT NULL DEFAULT '',  -- per-run execution credential (CLI 分支 Phase 2): issued at claim, sent to the executor, carried by the agent's CLI commands to /rpc; valid only while status='running'
    queued_at          TEXT NOT NULL,
    started_at         TEXT NOT NULL DEFAULT '',
    finished_at        TEXT NOT NULL DEFAULT '',
    -- P0-1 (决策 6-11): stamped INSIDE the reconcile transaction when the
    -- run's terminal outcome was reconciled. '' = terminal-but-unreconciled
    -- (daemon crash between the terminal UPDATE and the reconcile tx) — the
    -- startup replay (RunService.ReconcilePendingTerminal) re-runs the
    -- reconcile, which is safe: every transition is conditional and the
    -- stamp commits atomically with the run-report comment (no duplicates).
    reconciled_at      TEXT NOT NULL DEFAULT '',
    created_at         TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_run_goal ON run(goal_id);
CREATE INDEX IF NOT EXISTS idx_run_agent ON run(agent_id);
CREATE INDEX IF NOT EXISTS idx_run_status ON run(status);

-- sub_goal: a work item split off a goal (v2 model, 决策 6-1) — NOT a child
-- goal: no parent recursion, no own deliver/verification terminal semantics.
-- The goal stays the sole lifecycle authority; a sub-goal carries work
-- responsibility (assignee) + quality responsibility (verifier, '' = machine).
-- execution_attempt (machine retry ≤3) and quality_iteration (verifier
-- rejects, unbounded) are the AUTHORITATIVE counters (决策 6-9) — run.attempt
-- is just the instance ordinal. No run_id column: the relationship truth is
-- run.sub_goal_id → sub_goal.id (1:N); the active run is queried by status.
CREATE TABLE IF NOT EXISTS sub_goal (
    id                TEXT PRIMARY KEY,
    goal_id           TEXT NOT NULL REFERENCES goal(id),
    title             TEXT NOT NULL,
    description       TEXT NOT NULL DEFAULT '',
    assignee_id       TEXT NOT NULL,               -- the agent executing this work item
    verifier_id       TEXT NOT NULL DEFAULT '',    -- '' = machine (domain verify commands); else agent id
    status            TEXT NOT NULL DEFAULT 'running', -- running|verifying|verified|rejected|cancelled|failed（pending/done 保留在枚举注释但无写入路径：CreateSubGoal 直接 running，run 完成即 verifying/verified）
    execution_attempt INTEGER NOT NULL DEFAULT 0,
    quality_iteration INTEGER NOT NULL DEFAULT 0,
    depends_on        TEXT NOT NULL DEFAULT '[]', -- JSON array of sub_goal ids declared by the leader; '' edges are inferred at read time (workflow_edge)
    dispatch_comment_id TEXT NOT NULL DEFAULT '', -- the leader's dispatch comment for THIS work item ('' on legacy rows): a run born LATER (wakeDependentsTx, after the deps verify) carries it as run.trigger_comment_id, so the assignee's report still threads to the dispatch even when the first run was gated
    created_at        TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_sub_goal_goal ON sub_goal(goal_id, created_at);

-- change: a logical deliverable produced by a sub-goal (v2 model, 决策 6-3).
-- It is NOT a permanent branch: change_revision binds each revision to the
-- integration base it was built against. The Change row and its FIRST
-- revision are created atomically (Ready ⇔ a persisted Revision exists).
CREATE TABLE IF NOT EXISTS change (
    id          TEXT PRIMARY KEY,
    goal_id     TEXT NOT NULL REFERENCES goal(id),
    sub_goal_id TEXT NOT NULL REFERENCES sub_goal(id),
    status      TEXT NOT NULL DEFAULT 'ready', -- ready|integrating|integrated|conflict
    created_at  TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_change_goal ON change(goal_id, created_at);

CREATE TABLE IF NOT EXISTS change_revision (
    id         TEXT PRIMARY KEY,
    change_id  TEXT NOT NULL REFERENCES change(id),
    seq        INTEGER NOT NULL,        -- 1..N; conflict resolution appends N+1
    base_ref   TEXT NOT NULL,           -- the integration base this revision was built on
    head_ref   TEXT NOT NULL,           -- the revision's commit ref
    created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_change_revision_change ON change_revision(change_id, seq);

-- verification_result: one verification round of a sub-goal (v2 model,
-- 决策 6-5). Verifier runs produce verdicts via the structured tool; machine
-- verification (verifier_id='') records one row per run-end check. Rounds ≠
-- run.attempt (verifier runs retry their own machine failures separately).
CREATE TABLE IF NOT EXISTS verification_result (
    id              TEXT PRIMARY KEY,
    goal_id         TEXT NOT NULL REFERENCES goal(id),
    sub_goal_id     TEXT NOT NULL REFERENCES sub_goal(id),
    verifier_run_id TEXT NOT NULL DEFAULT '',
    status          TEXT NOT NULL, -- passed|rejected
    summary         TEXT NOT NULL DEFAULT '',
    evidence        TEXT NOT NULL DEFAULT '',
    created_at      TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_verification_result_sub ON verification_result(sub_goal_id, created_at);

-- handoff_event: one ownership transition (Collaboration.md §20). Append-only
-- audit of who handed the goal to whom, from/to which runs, and why — the
-- handoff-cycle signal (A→B→A→B ping-pong) is counted from here. to_run_id is
-- back-filled by the caller after the new owner's run is enqueued.
CREATE TABLE IF NOT EXISTS handoff_event (
    id          TEXT PRIMARY KEY,
    goal_id     TEXT NOT NULL REFERENCES goal(id),
    from_type   TEXT NOT NULL,               -- agent|squad|human (assignee is polymorphic)
    from_id     TEXT NOT NULL DEFAULT '',
    to_type     TEXT NOT NULL,
    to_id       TEXT NOT NULL DEFAULT '',
    from_run_id TEXT NOT NULL DEFAULT '',    -- the old owner's run terminated by the handoff
    to_run_id   TEXT NOT NULL DEFAULT '',    -- the new owner's run enqueued by the handoff
    reason      TEXT NOT NULL DEFAULT '',
    actor_type  TEXT NOT NULL DEFAULT 'human', -- who performed the handoff (agent|human|system)
    actor_id    TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_handoff_event_goal ON handoff_event(goal_id, created_at);

-- consult_request: one Consult (Collaboration.md §12) — an agent's mention of
-- another agent for information/judgment. Links the full chain:
-- trigger comment → guest run → response comment → (auto) requester resume.
CREATE TABLE IF NOT EXISTS consult_request (
    id                  TEXT PRIMARY KEY,
    goal_id             TEXT NOT NULL REFERENCES goal(id),
    requester_agent_id  TEXT NOT NULL DEFAULT '', -- '' = human/system-triggered (no auto-resume)
    requester_run_id    TEXT NOT NULL DEFAULT '', -- the owner run that asked
    target_agent_id     TEXT NOT NULL DEFAULT '',
    trigger_comment_id  TEXT NOT NULL DEFAULT '', -- the mention comment that pulled in the guest
    guest_run_id        TEXT NOT NULL DEFAULT '', -- the guest run answering the consult
    response_comment_id TEXT NOT NULL DEFAULT '', -- the guest report comment (back-filled at run end)
    created_at          TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_consult_request_guest ON consult_request(guest_run_id);
CREATE INDEX IF NOT EXISTS idx_consult_request_goal ON consult_request(goal_id, created_at);

-- gate_decision: checkpoint decision audit + the health-learning data source
-- (per-gate approve/reject ratios feed the "suggest dropping/ tightening a
-- gate" loop, DESIGN.md §13). Append-only.
CREATE TABLE IF NOT EXISTS gate_decision (
    id              TEXT PRIMARY KEY,
    goal_id         TEXT NOT NULL REFERENCES goal(id),
    run_id          TEXT NOT NULL DEFAULT '',   -- the run whose evidence the decision refers to
    gate_rule       TEXT NOT NULL,              -- which gate rule fired ("merge", "guard:config", ...)
    decision        TEXT NOT NULL,              -- approve|reject|redirect
    reason          TEXT NOT NULL DEFAULT '',
    decided_by      TEXT NOT NULL DEFAULT 'human',
    decided_at      TEXT NOT NULL,
    review_duration INTEGER NOT NULL DEFAULT 0  -- seconds spent in review before the decision
);

CREATE INDEX IF NOT EXISTS idx_gate_decision_goal ON gate_decision(goal_id, decided_at);

-- comment: a message under a goal. Authors are polymorphic (human | agent |
-- system). content is Markdown and may carry structured mention URIs:
--   [@Name](mention://agent/<uuid>)   → enqueue a new run on that agent
--   [@Name](mention://squad/<uuid>)   → route to the squad's leader
--   [@Name](mention://human/<uuid>)   → renders a link, no run
--   [@all](mention://all/all)         → suppress auto-trigger, notify humans
-- Mentions are parsed only from persisted comment bodies, never from agent
-- stdout (the agent drives triggers by calling agentwork-cli).
CREATE TABLE IF NOT EXISTS comment (
    id          TEXT PRIMARY KEY,
    goal_id     TEXT NOT NULL REFERENCES goal(id),
    author_type TEXT NOT NULL CHECK (author_type IN ('human', 'agent', 'system')),
    author_id   TEXT NOT NULL DEFAULT '',   -- zero id for system rows
    parent_id   TEXT REFERENCES comment(id), -- one level of threading
    content     TEXT NOT NULL,
    created_at  TEXT NOT NULL,
    run_id      TEXT NOT NULL DEFAULT '',  -- the run whose product this comment is ('' = trigger/context rows)
    ask_human   INTEGER NOT NULL DEFAULT 0  -- 决策 7-3: 1 = agent's question to the human (goal creator); notify pushes a Feishu card. Reply routing is by parent_id→agent, not this flag.
);

CREATE INDEX IF NOT EXISTS idx_comment_goal ON comment(goal_id, created_at);

-- chat_message: the run's output stream cache (tool calls, thoughts, text),
-- for the run detail view. Belongs to a run, not a goal.
CREATE TABLE IF NOT EXISTS chat_message (
    id         TEXT PRIMARY KEY,
    run_id     TEXT NOT NULL REFERENCES run(id),
    role       TEXT NOT NULL,              -- user|assistant|tool|system
    content    TEXT NOT NULL DEFAULT '',
    tool_calls TEXT NOT NULL DEFAULT '[]', -- JSON
    created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_chat_message_run ON chat_message(run_id, created_at);

-- Append-only audit trail. Handoffs, child creations, squad evaluations, ...
CREATE TABLE IF NOT EXISTS activity_log (
    id         TEXT PRIMARY KEY,
    goal_id    TEXT NOT NULL REFERENCES goal(id),
    actor_type TEXT NOT NULL,              -- agent|human|system
    actor_id   TEXT NOT NULL DEFAULT '',
    action     TEXT NOT NULL,             -- created|assigned|handoff|child_created|children_done|squad_leader_evaluated|cancelled|...
    detail     TEXT NOT NULL DEFAULT '{}', -- JSON
    created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_activity_goal ON activity_log(goal_id, created_at);

-- A schedule is a cron-triggered goal template. At each cron occurrence the
-- daemon clones a fresh goal row from the template fields, assigns it, and
-- enqueues a run — the normal dispatch chain then runs it. (Template + instance
-- model, like multica autopilot.) Idempotency via uq_schedule_run_planned.
CREATE TABLE IF NOT EXISTS schedule (
    id              TEXT PRIMARY KEY,
    name            TEXT NOT NULL,
    title_template  TEXT NOT NULL,             -- cloned goal title
    description     TEXT NOT NULL DEFAULT '',  -- cloned goal description
    assignee_type   TEXT NOT NULL DEFAULT 'agent', -- agent|squad
    assignee_id     TEXT NOT NULL,
    domain_id       TEXT REFERENCES domain(id),-- v2 (M1): fired goals belong to this domain
    cron_expression TEXT NOT NULL,             -- 5-field standard cron
    timezone        TEXT NOT NULL DEFAULT 'UTC',
    category        TEXT NOT NULL DEFAULT 'hitl', -- cloned goal category: hitl | hootl
    enabled         INTEGER NOT NULL DEFAULT 1,
    next_run_at     TEXT NOT NULL DEFAULT '',  -- next fire time RFC3339; '' = unset
    last_run_at     TEXT NOT NULL DEFAULT '',
    created_at      TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_schedule_enabled ON schedule(enabled, next_run_at);

-- schedule_run records each firing and the goal it produced (history +
-- idempotency: one firing per (schedule_id, planned_at)).
CREATE TABLE IF NOT EXISTS schedule_run (
    id          TEXT PRIMARY KEY,
    schedule_id TEXT NOT NULL REFERENCES schedule(id),
    goal_id     TEXT NOT NULL REFERENCES goal(id),
    planned_at  TEXT NOT NULL,                 -- cron occurrence that fired
    status      TEXT NOT NULL DEFAULT 'dispatched', -- dispatched|failed
    created_at  TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_schedule_run_schedule ON schedule_run(schedule_id, created_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS uq_schedule_run_planned ON schedule_run(schedule_id, planned_at);

-- team_import is a TEMPORARY tracking table for the team-definition-repo
-- import processor run: the run that clones a team repo, has the steward
-- agent explore it, and produces team.json (agents + skills + squad). The
-- platform reads team.json and upserts the entities by name. The steward
-- agent decides which runtime each imported agent binds to (from the team
-- definition or random assignment); runtime_id is NOT stored here.
-- git_url/credentials/branch persist from the HTTP request to daemon dispatch
-- (the run may sit queued for seconds/minutes before claim). ImportTeam
-- cleans up old completed/failed rows at the start of each import. status:
-- pending|completed|failed.
CREATE TABLE IF NOT EXISTS team_import (
    id              TEXT PRIMARY KEY,
    run_id          TEXT NOT NULL DEFAULT '',         -- the import run (back-filled after enqueue; may lag on reopen — use goal_id for lookups)
    goal_id         TEXT NOT NULL DEFAULT '',         -- the import goal (stable across reopen/retry — the authoritative key)
    git_url         TEXT NOT NULL DEFAULT '',         -- team repo URL (read at dispatch time)
    git_credentials TEXT NOT NULL DEFAULT '',         -- team repo token
    default_branch  TEXT NOT NULL DEFAULT '',         -- team repo branch
    status          TEXT NOT NULL DEFAULT 'pending',  -- pending|completed|failed
    result          TEXT NOT NULL DEFAULT '',         -- JSON summary (agents/skills/squad created/updated)
    created_at      TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_team_import_run ON team_import(run_id);

-- settings: key-value daemon configuration (e.g. the Feishu connection
-- credentials + receive target captured by the IM connect flow, M1). The
-- IM module persists here so the daemon auto-reconnects on startup with no
-- environment configuration.
CREATE TABLE IF NOT EXISTS app_settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL DEFAULT '',  -- JSON
    updated_at TEXT NOT NULL
);

-- agent_pin: sidebar pin state for agents (single-user — no user dimension).
-- Row existence = pinned: pin = INSERT OR IGNORE, unpin = DELETE, and
-- SELECT agent_id FROM agent_pin is the pinned set. The PK makes toggle
-- idempotent (one pin per agent). Cleaned up manually in
-- AgentService.Delete's dependent-cleanup loop — same non-cascading-FK
-- pattern as chat_message→run→agent (no ON DELETE CASCADE, matching the
-- predominant schema style; only squad_member uses CASCADE).
CREATE TABLE IF NOT EXISTS agent_pin (
    agent_id   TEXT NOT NULL REFERENCES agent(id),
    created_at TEXT NOT NULL,                 -- RFC3339Nano; pin time, drives sidebar order
    PRIMARY KEY (agent_id)
);
-- connection: a first-class squad ↔ repository binding. Each row owns its
-- OWN webhook path (/webhooks/{provider}/{id}) and secret, so two squads (or
-- one squad twice) can track the same repository without overwriting each
-- other's host hook / assignee. events is a comma-separated subset of
-- "issues,pull_request".
CREATE TABLE IF NOT EXISTS connection (
    id         TEXT PRIMARY KEY,
    squad_id   TEXT NOT NULL,
    domain_id  TEXT NOT NULL,
    provider   TEXT NOT NULL DEFAULT 'github',                    -- github | gitcode
    repo       TEXT NOT NULL,                                     -- owner/repo
    events     TEXT NOT NULL DEFAULT 'issues,pull_request',
    secret     TEXT NOT NULL DEFAULT '',                          -- per-connection signing secret
    created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_connection_squad ON connection(squad_id);
CREATE INDEX IF NOT EXISTS idx_connection_domain ON connection(domain_id);


-- ── RSI (recursive self-improvement) ─────────────────────────────────────
-- One training session per goal whose execution_mode='train'. Created with
-- the goal; at goal terminal state the workflow graph is frozen into
-- rsi_report.workflow_json and the structure analysis runs asynchronously.
-- waiting → analyzing → ready|failed (sampled-out goals end as 'skipped').
-- No span collection: W-RSI observes entity-level data only.
CREATE TABLE IF NOT EXISTS training_session (
    id              TEXT PRIMARY KEY,
    goal_id         TEXT NOT NULL REFERENCES goal(id),
    content_capture TEXT NOT NULL DEFAULT 'redacted',  -- none|redacted|full
    sample_rate     REAL NOT NULL DEFAULT 1,
    budget_usd      REAL NOT NULL DEFAULT 2,
    status          TEXT NOT NULL DEFAULT 'waiting',   -- waiting|analyzing|ready|failed|skipped
    node_count       INTEGER NOT NULL DEFAULT 0,       -- frozen workflow summary (filled at analysis)
    critical_path_ms INTEGER NOT NULL DEFAULT 0,
    parallel_speedup REAL NOT NULL DEFAULT 0,
    finding_count    INTEGER NOT NULL DEFAULT 0,
    error           TEXT NOT NULL DEFAULT '',
    started_at      TEXT NOT NULL,
    sealed_at       TEXT NOT NULL DEFAULT '',
    finished_at     TEXT NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_training_session_goal ON training_session(goal_id);
CREATE INDEX IF NOT EXISTS idx_training_session_status ON training_session(status, started_at);

-- workflow_edge: edges of the orchestration graph, stored separately from
-- nodes so the graph never duplicates execution state. Nodes are assembled
-- read-time from goal/sub_goal/run/verification_result/consult_request/change.
-- Node keys: goal | sg:<sub_goal_id> | verify:<sub_goal_id> | review:<run_id>
-- | consult:<run_id> | chg:<change_id>. kind: created|depends_on|seq|
-- verified_by|consult|integrated. source: explicit (declared dependency or
-- structural by construction) | inferred (time-order heuristic) — inferred
-- edges are dashed in the UI and only ever support L1 findings.
CREATE TABLE IF NOT EXISTS workflow_edge (
    id         TEXT PRIMARY KEY,
    goal_id    TEXT NOT NULL REFERENCES goal(id),
    from_node  TEXT NOT NULL,          -- node key: goal | run:<id> | sg:<id> | verify:<id> | change:<id>
    to_node    TEXT NOT NULL,
    kind       TEXT NOT NULL,
    source     TEXT NOT NULL DEFAULT 'inferred',  -- explicit|inferred
    confidence TEXT NOT NULL DEFAULT 'L1',
    created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_workflow_edge_goal ON workflow_edge(goal_id, created_at);
CREATE INDEX IF NOT EXISTS idx_workflow_edge_from ON workflow_edge(from_node);
CREATE INDEX IF NOT EXISTS idx_workflow_edge_to ON workflow_edge(to_node);

-- rsi_report: exactly one analysis output per session. metrics_json and
-- baseline_json are objects the API decodes before returning.
CREATE TABLE IF NOT EXISTS rsi_report (
    id            TEXT PRIMARY KEY,
    session_id    TEXT NOT NULL REFERENCES training_session(id),
    status        TEXT NOT NULL DEFAULT 'ready',   -- ready|failed
    summary       TEXT NOT NULL DEFAULT '',
    metrics_json  TEXT NOT NULL DEFAULT '{}',
    baseline_json TEXT NOT NULL DEFAULT '{}',
    workflow_json TEXT NOT NULL DEFAULT '{}',   -- frozen orchestration graph (nodes/edges/critical path) at analysis time
    narrative     TEXT NOT NULL DEFAULT '',
    error         TEXT NOT NULL DEFAULT '',
    created_at    TEXT NOT NULL,
    finished_at   TEXT NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_rsi_report_session ON rsi_report(session_id);

-- rsi_finding: one rule-based candidate optimization. confidence is
-- L1 (observed once) | L2 (recurring pattern) | L3 (A/B verified). evidence_json
-- is a JSON array of span/run ids + measurement text.
CREATE TABLE IF NOT EXISTS rsi_finding (
    id          TEXT PRIMARY KEY,
    report_id   TEXT NOT NULL REFERENCES rsi_report(id),
    category    TEXT NOT NULL,
    severity    TEXT NOT NULL DEFAULT 'info',      -- info|warn|critical
    confidence  TEXT NOT NULL DEFAULT 'L1',
    title       TEXT NOT NULL,
    explanation TEXT NOT NULL DEFAULT '',
    suggestion  TEXT NOT NULL DEFAULT '',
    evidence_json TEXT NOT NULL DEFAULT '[]',
    score       REAL NOT NULL DEFAULT 0,
    status      TEXT NOT NULL DEFAULT 'open',      -- open|confirmed|dismissed
    created_at  TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_rsi_finding_report ON rsi_finding(report_id, score DESC);
CREATE INDEX IF NOT EXISTS idx_rsi_finding_category ON rsi_finding(category, created_at);

-- rsi_round: the optimization-round ledger for the insight panel. Every
-- analysis run appends one row — frozen metrics + baseline, the delta vs the
-- previous round, and a frozen copy of that round's findings. The live
-- rsi_report/rsi_finding rows always mirror the LATEST round; history lives
-- only here. rsi_metric_snapshot (v1, never written) is retired — trend data
-- derives from this ledger instead.
CREATE TABLE IF NOT EXISTS rsi_round (
    id               TEXT PRIMARY KEY,
    session_id       TEXT NOT NULL REFERENCES training_session(id),
    goal_id          TEXT NOT NULL,
    round_no         INTEGER NOT NULL,               -- 1-based, per domain (not per session)
    trigger          TEXT NOT NULL DEFAULT 'terminal', -- terminal | manual_retry
    metrics_json     TEXT NOT NULL DEFAULT '{}',
    baseline_json    TEXT NOT NULL DEFAULT '{}',
    delta_json       TEXT NOT NULL DEFAULT '{}',     -- {key:{from,to,pct}} vs the previous round in the same domain; '{}' for the first
    finding_count    INTEGER NOT NULL DEFAULT 0,
    critical_count   INTEGER NOT NULL DEFAULT 0,     -- severity=critical findings
    top_category     TEXT NOT NULL DEFAULT '',
    top_title        TEXT NOT NULL DEFAULT '',
    findings_json    TEXT NOT NULL DEFAULT '[]',     -- frozen []rsi_finding copy
    summary          TEXT NOT NULL DEFAULT '',
    narrative        TEXT NOT NULL DEFAULT '',
    created_at       TEXT NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_rsi_round_session ON rsi_round(session_id, round_no);
CREATE INDEX IF NOT EXISTS idx_rsi_round_goal ON rsi_round(goal_id, created_at);
