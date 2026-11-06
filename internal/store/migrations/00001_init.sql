-- +goose Up
create extension if not exists vector;

-- +goose StatementBegin
create function set_updated_at() returns trigger language plpgsql as $$
begin
    new.updated_at = now();
    return new;
end;
$$;
-- +goose StatementEnd

-- Agents -------------------------------------------------------------------

create table agents (
    id              uuid primary key default gen_random_uuid(),
    name            text not null unique,
    description     text not null default '',
    current_version int,
    created_at      timestamptz not null default now(),
    updated_at      timestamptz not null default now()
);

-- Immutable once written. A change to an agent is a new version.
create table agent_versions (
    agent_id      uuid not null references agents(id) on delete cascade,
    version       int  not null check (version > 0),
    system_prompt text not null,
    model         text not null,                -- OpenRouter slug, e.g. 'openai/gpt-4.1-mini'
    tools         text[] not null default '{}',
    config        jsonb  not null default '{}', -- temperature, max tokens, etc.
    notes         text   not null default '',   -- why this version exists
    created_at    timestamptz not null default now(),
    primary key (agent_id, version)
);

alter table agents
    add foreign key (id, current_version) references agent_versions(agent_id, version);

create trigger agents_updated_at before update on agents
    for each row execute function set_updated_at();

-- Workflows ----------------------------------------------------------------

-- What a workflow is for. This row is what gets searched.
create table workflows (
    id              uuid primary key default gen_random_uuid(),
    name            text not null unique,
    description     text not null,
    current_version int,
    embedding       vector(1536),               -- of name + description; null until embedded
    embedding_model text,
    search          tsvector generated always as
                    (to_tsvector('english', name || ' ' || description)) stored,
    created_at      timestamptz not null default now(),
    updated_at      timestamptz not null default now()
);

-- How a workflow runs: a DAG of nodes. Immutable once written.
create table workflow_versions (
    workflow_id uuid  not null references workflows(id) on delete cascade,
    version     int   not null check (version > 0),
    graph       jsonb not null check (jsonb_typeof(graph) = 'object'),
    notes       text  not null default '',
    created_at  timestamptz not null default now(),
    primary key (workflow_id, version)
);

alter table workflows
    add foreign key (id, current_version) references workflow_versions(workflow_id, version);

create trigger workflows_updated_at before update on workflows
    for each row execute function set_updated_at();

create index workflows_embedding_idx on workflows using hnsw (embedding vector_cosine_ops);
create index workflows_search_idx    on workflows using gin (search);

-- Memories -----------------------------------------------------------------

create table memories (
    id              uuid primary key default gen_random_uuid(),
    content         text   not null,
    tags            text[] not null default '{}',
    source          text   not null default '', -- 'user', 'agent:<name>', 'workflow:<name>'
    metadata        jsonb  not null default '{}',
    embedding       vector(1536),
    embedding_model text,
    search          tsvector generated always as
                    (to_tsvector('english', content)) stored,
    created_at      timestamptz not null default now(),
    updated_at      timestamptz not null default now()
);

create trigger memories_updated_at before update on memories
    for each row execute function set_updated_at();

create index memories_embedding_idx on memories using hnsw (embedding vector_cosine_ops);
create index memories_search_idx    on memories using gin (search);
create index memories_tags_idx      on memories using gin (tags);

-- +goose Down
drop table memories;
drop table workflows cascade;
drop table workflow_versions;
drop table agents cascade;
drop table agent_versions;
drop function set_updated_at();
