-- name: CreateAgent :one
insert into agents (name, description)
values ($1, $2)
returning *;

-- name: GetAgentByName :one
select *
from agents
where name = $1;

-- name: GetAgentByNameForUpdate :one
-- Locks the agent so concurrent version writes are serialised.
select *
from agents
where name = $1
for update;

-- name: CreateAgentIfMissing :one
-- Returns no row if an agent with this name already exists.
insert into agents (name, description)
values ($1, $2)
on conflict (name) do nothing
returning *;

-- name: ListAgents :many
select *
from agents
order by name;

-- name: CreateAgentVersion :one
-- Appends the next version number for the agent.
insert into agent_versions (agent_id, version, system_prompt, model, config, output_schema, notes)
select sqlc.arg(agent_id), coalesce(max(version), 0) + 1,
       sqlc.arg(system_prompt), sqlc.arg(model),
       sqlc.arg(config), sqlc.narg(output_schema), sqlc.arg(notes)
from agent_versions
where agent_id = sqlc.arg(agent_id)
returning *;

-- name: GetAgentsByNames :many
select * from agents where name = any(sqlc.arg(names)::text[]);

-- name: SetAgentAdmin :exec
-- Not exposed as a tool. Only writes when the flag changes.
update agents set admin = $2 where id = $1 and admin <> $2;

-- name: SetAgentCurrentVersion :exec
update agents set current_version = $2 where id = $1;

-- name: GetAgentVersion :one
select * from agent_versions where agent_id = $1 and version = $2;

-- name: GetCurrentAgentVersion :one
select v.*
from agents a
join agent_versions v on v.agent_id = a.id and v.version = a.current_version
where a.name = $1;

-- name: ListAgentVersions :many
select * from agent_versions where agent_id = $1 order by version desc;
