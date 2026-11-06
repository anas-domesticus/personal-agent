-- +goose Up

-- Conversations ---------------------------------------------------------------

create table conversations (
    id         uuid primary key default gen_random_uuid(),
    agent_id   uuid not null references agents(id),
    title      text not null default '',
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now()
);

create trigger conversations_updated_at before update on conversations
    for each row execute function set_updated_at();

create index conversations_recent_idx on conversations (updated_at desc);

-- One row per Eino schema.Message, in order. The full message (including tool
-- calls and tool results) is kept in `message` so history round-trips exactly.
create table messages (
    id              bigint generated always as identity primary key,
    conversation_id uuid  not null references conversations(id) on delete cascade,
    role            text  not null check (role in ('system', 'user', 'assistant', 'tool')),
    content         text  not null default '',
    message         jsonb not null,
    agent_version   int,  -- the agent version that produced it (assistant and tool messages)
    created_at      timestamptz not null default now()
);

create index messages_conversation_idx on messages (conversation_id, id);

-- Runs -----------------------------------------------------------------------

create table runs (
    id               uuid primary key default gen_random_uuid(),
    workflow_id      uuid not null,
    workflow_version int  not null,
    status           text not null default 'queued'
                     check (status in ('queued', 'running', 'succeeded', 'failed')),
    input            jsonb not null default '{}' check (jsonb_typeof(input) = 'object'),
    output           jsonb,
    error            text,
    triggered_by     text not null default '',  -- e.g. 'agent:admin', 'user'
    created_at       timestamptz not null default now(),
    started_at       timestamptz,
    finished_at      timestamptz,
    foreign key (workflow_id, workflow_version) references workflow_versions(workflow_id, version)
);

create index runs_workflow_idx on runs (workflow_id, created_at desc);
create index runs_recent_idx   on runs (created_at desc);

-- One row per node. Records the exact agent version the node ran with, since
-- nodes always use the agent's version that is current when they start.
create table run_steps (
    id                bigint generated always as identity primary key,
    run_id            uuid not null references runs(id) on delete cascade,
    node_id           text not null,
    agent_id          uuid,
    agent_version     int,
    status            text not null default 'running'
                      check (status in ('running', 'succeeded', 'failed')),
    input             jsonb not null,
    output            jsonb,
    error             text,
    messages          jsonb not null default '[]',  -- the agent's full transcript
    prompt_tokens     int not null default 0,
    completion_tokens int not null default 0,
    started_at        timestamptz not null default now(),
    finished_at       timestamptz,
    unique (run_id, node_id),
    foreign key (agent_id, agent_version) references agent_versions(agent_id, version)
);

-- +goose Down
drop table run_steps;
drop table runs;
drop table messages;
drop table conversations;
