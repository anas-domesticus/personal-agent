-- +goose Up

-- Dropping the table also drops its trigger and indexes.
drop table memories;

-- +goose Down

create table memories (
    id              uuid primary key default gen_random_uuid(),
    content         text   not null,
    tags            text[] not null default '{}',
    source          text   not null default '',
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
