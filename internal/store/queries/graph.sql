-- name: InsertGraphNodeType :one
insert into graph_node_types (kind, versioned, data_schema)
values ($1, $2, $3)
on conflict (kind) do nothing
returning *;

-- name: GetGraphNodeType :one
select *
from graph_node_types
where kind = $1;

-- name: ListGraphNodeTypes :many
select *
from graph_node_types
order by kind;

-- name: InsertGraphEdgeType :one
insert into graph_edge_types (kind, data_schema)
values ($1, $2)
on conflict (kind) do nothing
returning *;

-- name: GetGraphEdgeType :one
select *
from graph_edge_types
where kind = $1;

-- name: ListGraphEdgeTypes :many
select *
from graph_edge_types
order by kind;

-- name: InsertGraphEdgeEndpoint :exec
insert into graph_edge_type_endpoints (edge_kind, source_kind, target_kind)
values ($1, $2, $3)
on conflict do nothing;

-- name: ListGraphEdgeEndpoints :many
select edge_kind, source_kind, target_kind
from graph_edge_type_endpoints
where edge_kind = $1
order by source_kind, target_kind;

-- name: InsertGraphNode :one
insert into graph_nodes (id, kind, versioned, data, current_version)
values ($1, $2, $3, $4, $5)
returning *;

-- name: GetGraphNode :one
select *
from graph_nodes
where id = $1 and kind = $2;

-- name: GetGraphNodeCurrent :one
select n.id, n.kind, coalesce(n.current_version, 0)::bigint as current_version,
       n.revision, coalesce(v.data, n.data) as data,
       cast(coalesce(v.data, n.data)->>'title' as text) as title,
       cast(coalesce(v.data, n.data)->>'description' as text) as description,
       n.created_at, n.updated_at
from graph_nodes n
left join graph_node_versions v on v.node_id = n.id and v.version = n.current_version
where n.id = $1 and n.kind = $2;

-- name: GetGraphNodeForUpdate :one
select *
from graph_nodes
where id = $1 and kind = $2
for update;

-- name: ListGraphNodes :many
select n.id, n.kind, coalesce(n.current_version, 0)::bigint as current_version,
       n.revision, coalesce(v.data, n.data) as data,
       cast(coalesce(v.data, n.data)->>'title' as text) as title,
       cast(coalesce(v.data, n.data)->>'description' as text) as description,
       n.created_at, n.updated_at
from graph_nodes n
left join graph_node_versions v on v.node_id = n.id and v.version = n.current_version
where (sqlc.arg(all_kinds)::bool or n.kind = sqlc.arg(kind)::text)
order by n.created_at, n.id
limit sqlc.arg(lim)::int;

-- name: UpdateGraphNodeData :one
update graph_nodes
set data = $3, revision = revision + 1, updated_at = now(),
    title_embedding = null, description_embedding = null, embedding_model = null, embedding_revision = null
where id = $1 and kind = $2
returning *;

-- name: SetGraphNodeVersion :one
update graph_nodes
set current_version = $3, revision = revision + 1, updated_at = now(),
    title_embedding = null, description_embedding = null, embedding_model = null, embedding_revision = null
where id = $1 and kind = $2
returning *;

-- name: DeleteGraphNode :execrows
delete from graph_nodes
where id = sqlc.arg(id) and kind = sqlc.arg(kind)
  and (not sqlc.arg(check_revision)::bool or revision = sqlc.arg(expected_revision)::bigint);

-- name: InsertGraphNodeVersion :one
insert into graph_node_versions (node_id, version, data, metadata)
values ($1, $2, $3, $4)
returning *;

-- name: GetGraphNodeVersion :one
select *
from graph_node_versions
where node_id = $1 and version = $2;

-- name: GetGraphNodeMaxVersion :one
select coalesce(max(version), 0)::bigint
from graph_node_versions
where node_id = $1;

-- name: ListGraphNodeVersions :many
select *
from graph_node_versions
where node_id = $1 and ($2::bigint = 0 or version < $2)
order by version desc
limit $3;

-- name: InsertGraphEdge :one
insert into graph_edges (id, kind, source_id, source_kind, target_id, target_kind, current_version)
values ($1, $2, $3, $4, $5, $6, 1)
returning *;

-- name: GetGraphEdge :one
select e.id, e.kind, e.current_version, e.source_id, e.source_kind,
       e.target_id, e.target_kind, v.data, e.revision, e.created_at, e.updated_at
from graph_edges e
join graph_edge_versions v on v.edge_id = e.id and v.version = e.current_version
where e.id = $1 and e.kind = $2;

-- name: GetGraphEdgeBetween :one
select e.id, e.kind, e.current_version, e.source_id, e.source_kind,
       e.target_id, e.target_kind, v.data, e.revision, e.created_at, e.updated_at
from graph_edges e
join graph_edge_versions v on v.edge_id = e.id and v.version = e.current_version
where e.kind = $1 and e.source_id = $2 and e.target_id = $3;

-- name: LockGraphEdgeTuple :exec
select pg_advisory_xact_lock(hashtextextended(sqlc.arg(lock_key)::text, 0));

-- name: GetGraphEdgeBetweenForUpdate :one
select *
from graph_edges
where kind = $1 and source_id = $2 and target_id = $3
for update;

-- name: SetGraphEdgeVersion :one
update graph_edges
set current_version = $2, revision = revision + 1, updated_at = now()
where id = $1
returning *;

-- name: DeleteGraphEdge :execrows
delete from graph_edges
where id = sqlc.arg(id) and kind = sqlc.arg(kind)
  and (not sqlc.arg(check_revision)::bool or revision = sqlc.arg(expected_revision)::bigint);

-- name: ListGraphEdgesFrom :many
select e.id, e.kind, e.current_version, e.source_id, e.source_kind,
       e.target_id, e.target_kind, v.data, e.revision, e.created_at, e.updated_at
from graph_edges e
join graph_edge_versions v on v.edge_id = e.id and v.version = e.current_version
where e.kind = $1 and e.source_id = $2
order by e.created_at, e.id;

-- name: ListGraphEdgesTo :many
select e.id, e.kind, e.current_version, e.source_id, e.source_kind,
       e.target_id, e.target_kind, v.data, e.revision, e.created_at, e.updated_at
from graph_edges e
join graph_edge_versions v on v.edge_id = e.id and v.version = e.current_version
where e.kind = $1 and e.target_id = $2
order by e.created_at, e.id;

-- name: ListGraphEdges :many
select e.id, e.kind, e.current_version, e.source_id, e.source_kind,
       e.target_id, e.target_kind, v.data, e.revision, e.created_at, e.updated_at
from graph_edges e
join graph_edge_versions v on v.edge_id = e.id and v.version = e.current_version
where (sqlc.arg(all_kinds)::bool or e.kind = sqlc.arg(kind)::text)
  and (sqlc.arg(all_sources)::bool or e.source_id = sqlc.arg(source_id)::uuid)
  and (sqlc.arg(all_targets)::bool or e.target_id = sqlc.arg(target_id)::uuid)
order by e.created_at, e.id
limit sqlc.arg(lim)::int;

-- name: InsertGraphEdgeVersion :one
insert into graph_edge_versions (edge_id, version, data, metadata)
values ($1, $2, $3, $4)
returning *;

-- name: GetGraphEdgeVersion :one
select *
from graph_edge_versions
where edge_id = $1 and version = $2;

-- name: GetGraphEdgeMaxVersion :one
select coalesce(max(version), 0)::bigint
from graph_edge_versions
where edge_id = $1;

-- name: ListGraphEdgeVersions :many
select *
from graph_edge_versions
where edge_id = $1 and ($2::bigint = 0 or version < $2)
order by version desc
limit $3;

-- name: GetGraphNodeEmbeddingText :one
select n.id, n.kind, n.revision,
       cast(coalesce(v.data, n.data)->>'title' as text) as title,
       cast(coalesce(v.data, n.data)->>'description' as text) as description
from graph_nodes n
left join graph_node_versions v on v.node_id = n.id and v.version = n.current_version
where n.id = $1 and n.kind = $2;

-- name: SetGraphNodeEmbeddings :execrows
update graph_nodes
set title_embedding = sqlc.arg(title_embedding),
    description_embedding = sqlc.arg(description_embedding),
    embedding_model = sqlc.arg(embedding_model),
    embedding_revision = sqlc.arg(embedding_revision)
where id = sqlc.arg(id) and kind = sqlc.arg(kind) and revision = sqlc.arg(revision);

-- name: ListGraphNodesMissingEmbeddings :many
select id, kind as node_kind, revision
from graph_nodes
where title_embedding is null or description_embedding is null
   or embedding_model is distinct from sqlc.arg(model)::text
   or embedding_revision is distinct from revision
limit sqlc.arg(lim);

-- name: SearchGraphNodes :many
select id, kind, current_version, revision, data, title, description, created_at, updated_at, score
from (
    select n.id, n.kind, coalesce(n.current_version, 0)::bigint as current_version,
           n.revision, coalesce(v.data, n.data) as data,
           cast(coalesce(v.data, n.data)->>'title' as text) as title,
           cast(coalesce(v.data, n.data)->>'description' as text) as description,
           n.created_at, n.updated_at,
           (1 - least(n.title_embedding <=> sqlc.arg(embedding)::vector,
                      n.description_embedding <=> sqlc.arg(embedding)::vector))::float8 as score,
           least(n.title_embedding <=> sqlc.arg(embedding)::vector,
                 n.description_embedding <=> sqlc.arg(embedding)::vector) as distance
    from graph_nodes n
    left join graph_node_versions v on v.node_id = n.id and v.version = n.current_version
    where n.embedding_model = sqlc.arg(model)::text
      and n.embedding_revision = n.revision
      and n.title_embedding is not null and n.description_embedding is not null
      and (sqlc.arg(all_kinds)::bool or n.kind = sqlc.arg(kind)::text)
) matches
order by distance
limit sqlc.arg(lim)::int;
