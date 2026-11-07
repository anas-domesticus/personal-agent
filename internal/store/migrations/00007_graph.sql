-- +goose Up

create table graph_node_types (
    kind        text primary key check (kind ~ '^[a-z][a-z0-9_-]{0,62}$'),
    versioned   boolean not null,
    data_schema jsonb not null check (jsonb_typeof(data_schema) = 'object'),
    created_at  timestamptz not null default now(),
    updated_at  timestamptz not null default now(),
    unique (kind, versioned)
);

create table graph_nodes (
    id              uuid primary key default gen_random_uuid(),
    kind            text not null,
    versioned       boolean not null,
    data            jsonb,
    current_version bigint,
    revision        bigint not null default 1 check (revision > 0),
    created_at      timestamptz not null default now(),
    updated_at      timestamptz not null default now(),
    unique (id, kind),
    foreign key (kind, versioned) references graph_node_types (kind, versioned),
    check (
        (not versioned and data is not null and current_version is null)
        or
        (versioned and data is null and current_version is not null and current_version > 0)
    )
);

create index graph_nodes_kind_idx on graph_nodes (kind, updated_at desc);

create table graph_node_versions (
    node_id    uuid not null references graph_nodes (id) on delete cascade,
    version    bigint not null check (version > 0),
    data       jsonb not null,
    metadata   jsonb not null default '{}' check (jsonb_typeof(metadata) = 'object'),
    created_at timestamptz not null default now(),
    primary key (node_id, version)
);

alter table graph_nodes
    add constraint graph_nodes_current_version_fk
    foreign key (id, current_version)
    references graph_node_versions (node_id, version)
    deferrable initially deferred;

create trigger graph_node_versions_immutable before update on graph_node_versions
    for each row execute function forbid_version_change();

create table graph_edge_types (
    kind        text primary key check (kind ~ '^[a-z][a-z0-9_-]{0,62}$'),
    data_schema jsonb not null check (jsonb_typeof(data_schema) = 'object'),
    created_at  timestamptz not null default now(),
    updated_at  timestamptz not null default now()
);

create table graph_edge_type_endpoints (
    edge_kind   text not null references graph_edge_types (kind),
    source_kind text not null references graph_node_types (kind),
    target_kind text not null references graph_node_types (kind),
    primary key (edge_kind, source_kind, target_kind)
);

create table graph_edges (
    id          uuid primary key default gen_random_uuid(),
    kind        text not null,
    source_id   uuid not null,
    source_kind text not null,
    target_id   uuid not null,
    target_kind text not null,
    data        jsonb not null default '{}',
    revision    bigint not null default 1 check (revision > 0),
    created_at  timestamptz not null default now(),
    updated_at  timestamptz not null default now(),
    unique (kind, source_id, target_id),
    check (source_id <> target_id),
    foreign key (kind, source_kind, target_kind)
        references graph_edge_type_endpoints (edge_kind, source_kind, target_kind),
    foreign key (source_id, source_kind) references graph_nodes (id, kind),
    foreign key (target_id, target_kind) references graph_nodes (id, kind)
);

create index graph_edges_source_idx on graph_edges (source_id, kind);
create index graph_edges_target_idx on graph_edges (target_id, kind);

-- +goose Down

drop table graph_edges;
drop table graph_edge_type_endpoints;
drop table graph_edge_types;
alter table graph_nodes drop constraint graph_nodes_current_version_fk;
drop trigger graph_node_versions_immutable on graph_node_versions;
drop table graph_node_versions;
drop table graph_nodes;
drop table graph_node_types;
