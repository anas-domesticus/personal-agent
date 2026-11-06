-- name: CreateWorkflow :one
insert into workflows (name, description)
values ($1, $2)
returning id, name, description, current_version, embedding_model, created_at, updated_at;

-- name: GetWorkflowByName :one
select id, name, description, current_version, embedding_model, created_at, updated_at
from workflows
where name = $1;

-- name: ListWorkflows :many
select id, name, description, current_version, embedding_model, created_at, updated_at
from workflows
order by name;

-- name: UpdateWorkflowDescription :exec
-- Clears the embedding so it is recomputed for the new text.
update workflows
set description = $2, embedding = null, embedding_model = null
where id = $1;

-- name: GetWorkflowText :one
select id, name, description from workflows where id = $1;

-- name: SetWorkflowEmbedding :execrows
-- Only writes if the text is still what was embedded; returns 0 rows otherwise.
update workflows
set embedding = sqlc.arg(embedding), embedding_model = sqlc.arg(embedding_model)
where id = sqlc.arg(id) and name = sqlc.arg(name) and description = sqlc.arg(description);

-- name: ListWorkflowsMissingEmbedding :many
select id, name, description
from workflows
where embedding is null or embedding_model is distinct from sqlc.arg(model)::text
limit sqlc.arg(lim);

-- name: CreateWorkflowVersion :one
-- Appends the next version number for the workflow.
insert into workflow_versions (workflow_id, version, graph, notes)
select sqlc.arg(workflow_id), coalesce(max(version), 0) + 1, sqlc.arg(graph), sqlc.arg(notes)
from workflow_versions
where workflow_id = sqlc.arg(workflow_id)
returning *;

-- name: SetWorkflowCurrentVersion :exec
update workflows set current_version = $2 where id = $1;

-- name: GetWorkflowVersion :one
select * from workflow_versions where workflow_id = $1 and version = $2;

-- name: GetCurrentWorkflowVersion :one
select v.*
from workflows w
join workflow_versions v on v.workflow_id = w.id and v.version = w.current_version
where w.name = $1;

-- name: ListWorkflowVersions :many
select * from workflow_versions where workflow_id = $1 order by version desc;

-- name: SearchWorkflows :many
-- Hybrid search: reciprocal rank fusion of vector similarity and full-text rank.
with semantic as (
    select id, row_number() over (order by distance) as rank
    from (
        select id, embedding <=> sqlc.arg(embedding)::vector as distance
        from workflows
        where embedding is not null
        order by embedding <=> sqlc.arg(embedding)::vector
        limit sqlc.arg(candidates)::int
    ) s
),
keyword as (
    select id, row_number() over (order by ts_rank_cd(search, q) desc) as rank
    from workflows, websearch_to_tsquery('english', sqlc.arg(query)::text) q
    where search @@ q
    limit sqlc.arg(candidates)::int
)
select w.id, w.name, w.description, w.current_version,
       (coalesce(1.0 / (60 + s.rank), 0) + coalesce(1.0 / (60 + k.rank), 0))::float8 as score
from semantic s
full outer join keyword k on k.id = s.id
join workflows w on w.id = coalesce(s.id, k.id)
order by score desc
limit sqlc.arg(lim)::int;
