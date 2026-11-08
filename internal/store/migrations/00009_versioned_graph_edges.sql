-- +goose Up

alter table graph_edges
    add column current_version bigint;

create table graph_edge_versions (
    edge_id    uuid not null references graph_edges (id) on delete cascade,
    version    bigint not null check (version > 0),
    data       jsonb not null check (jsonb_typeof(data) = 'object'),
    metadata   jsonb not null default '{}' check (jsonb_typeof(metadata) = 'object'),
    created_at timestamptz not null default now(),
    primary key (edge_id, version)
);

insert into graph_edge_versions (edge_id, version, data, created_at)
select id, 1, data, created_at
from graph_edges;

update graph_edges
set current_version = 1;

alter table graph_edges
    alter column current_version set not null,
    add constraint graph_edges_current_version_positive check (current_version > 0),
    add constraint graph_edges_current_version_fk
        foreign key (id, current_version)
        references graph_edge_versions (edge_id, version)
        deferrable initially deferred;

create trigger graph_edge_versions_immutable before update on graph_edge_versions
    for each row execute function forbid_version_change();

alter table graph_edges drop column data;

-- +goose Down

alter table graph_edges add column data jsonb not null default '{}';

update graph_edges e
set data = v.data
from graph_edge_versions v
where v.edge_id = e.id and v.version = e.current_version;

alter table graph_edges
    drop constraint graph_edges_current_version_fk,
    drop constraint graph_edges_current_version_positive,
    drop column current_version;

drop table graph_edge_versions;
