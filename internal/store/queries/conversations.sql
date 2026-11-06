-- name: CreateConversation :one
insert into conversations (agent_id, title, parent_id, subagent)
values (sqlc.narg(agent_id), sqlc.arg(title), sqlc.narg(parent_id), sqlc.narg(subagent))
returning *;

-- name: GetConversation :one
select c.*, a.name as agent_name
from conversations c
left join agents a on a.id = c.agent_id
where c.id = $1;

-- name: ListConversations :many
-- Top-level conversations only; subagent conversations hang off them.
select c.*, a.name as agent_name
from conversations c
left join agents a on a.id = c.agent_id
where c.parent_id is null and c.subagent is null
order by c.updated_at desc
limit $1;

-- name: SetConversationTitle :exec
update conversations set title = $2 where id = $1;

-- name: TouchConversation :exec
update conversations set updated_at = now() where id = $1;

-- name: AddMessage :one
insert into messages (conversation_id, role, content, message, agent_version)
values ($1, $2, $3, $4, $5)
returning id;

-- name: ListMessages :many
select * from messages
where conversation_id = $1
order by id;

-- name: LockConversation :one
-- Locks the conversation and returns its last message id (0 if none), so a
-- turn can check nothing was added while the agent was replying.
select coalesce((select max(m.id) from messages m where m.conversation_id = c.id), 0)::bigint as last_message_id
from conversations c
where c.id = $1
for update of c;
